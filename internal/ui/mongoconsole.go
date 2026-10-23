package ui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/console"
	"github.com/Snarap1/hqdb/internal/consolestate"
	"github.com/Snarap1/hqdb/internal/mongocmd"
	"github.com/Snarap1/hqdb/internal/mongoshell"
	"github.com/Snarap1/hqdb/internal/values"
)

// mongoConsoleTab is a mongodb Console (ticket 22), the SQL Console's
// sibling: the same consoleCore (editor, history, write confirmation,
// Ctrl+C cancellation), but its statements are the mongosh subset
// mongoshell.ParseCommand accepts, run through mongocmd.RunOrdered on the
// Profile's shared connection, with Documents shown as the Data view's
// cards (or grid). An aggregation with a writing stage
// (mongocmd.PipelineViolation) is refused on a read-only Profile and asks
// first on a read-write one (ADR-0003).
type mongoConsoleTab struct {
	consoleCore
	// database is the Database statements run against: the one the
	// Console was opened on, switched by "use <db>"; "" means the
	// Profile's configured Database.
	database string

	// hasDocs is set while cards (or the grid) show the last result.
	hasDocs  bool
	cards    docCards
	gridMode bool
	grid     grid
}

func newMongoConsoleTab(profile, database string, state *consolestate.Store) *mongoConsoleTab {
	t := &mongoConsoleTab{database: database, cards: newDocCards()}
	t.init(profile, "MONGO", state)
	return t
}

func (t *mongoConsoleTab) gridModal() bool { return t.gridMode && t.grid.modal() }

func (t *mongoConsoleTab) EditingText() bool { return t.editingText(t.gridModal()) }

// mongoConsoleMsg reports one statement's outcome.
type mongoConsoleMsg struct {
	tab       *mongoConsoleTab
	op        string
	res       *mongocmd.Result
	took      time.Duration
	err       error
	cancelled bool
}

func applyMongoConsoleMsg(msg mongoConsoleMsg) {
	t := msg.tab
	if !t.finish(msg.err, msg.cancelled, "statement cancelled") {
		return
	}
	took := formatDuration(msg.took)
	switch msg.op {
	case "count":
		t.hasDocs = false
		t.status = fmt.Sprintf("count: %d in %s", msg.res.Count, took)
		return
	case "distinct":
		// A distinct value is a scalar; a card per value keeps it
		// browsable and copyable like a Document.
		docs := make([]any, len(msg.res.Documents))
		for i, v := range msg.res.Documents {
			docs[i] = values.Doc{{Key: "value", Value: v}}
		}
		t.showDocs(docs, 0)
		t.status = fmt.Sprintf("%d values in %s", len(docs), took)
	default:
		t.showDocs(msg.res.Documents, 0)
		t.status = fmt.Sprintf("%d documents in %s", len(msg.res.Documents), took)
	}
	if msg.res.Truncated {
		t.status += fmt.Sprintf(" - more exist, truncated at %d", console.PreviewLimit)
	}
}

func (t *mongoConsoleTab) showDocs(docs []any, skip int) {
	t.cards = newDocCards()
	t.cards.docs, t.cards.skip = docs, skip
	t.grid = mongoGrid(docs)
	t.hasDocs = true
}

// runCmd parses the statement under the editor's cursor and runs it. A
// "use" switches the Database without a round trip.
func (t *mongoConsoleTab) runCmd(pool *connPool) tea.Cmd {
	stmt := t.statementAt(console.StatementAt)
	if stmt == "" {
		return nil
	}
	cmd, err := mongoshell.ParseCommand(stmt)
	if err != nil {
		t.err, t.status = err, ""
		return nil
	}
	if cmd.Use != "" {
		t.hist.record(stmt)
		t.database, t.err, t.status = cmd.Use, nil, "switched to db "+cmd.Use
		return nil
	}
	req := cmd.Request
	req.Database = t.database
	if req.Operation != "count" && (req.Limit == 0 || req.Limit > console.PreviewLimit) {
		req.Limit = console.PreviewLimit
	}
	return t.runOrAsk(stmt, mongoViolation(req), func() tea.Cmd { return t.execCmd(pool, stmt, req) })
}

// mongoViolation is the writing stage of an aggregation that the MCP
// server would refuse, or "" for anything else.
func mongoViolation(req mongocmd.Request) string {
	if req.Operation != "aggregate" {
		return ""
	}
	p, err := mongocmd.ParsePipeline(req.Pipeline)
	if err != nil {
		return ""
	}
	return mongocmd.PipelineViolation(p)
}

// execCmd runs req, unscreened.
func (t *mongoConsoleTab) execCmd(pool *connPool, stmt string, req mongocmd.Request) tea.Cmd {
	profile := t.profile
	return t.exec(stmt, func(ctx context.Context) tea.Msg {
		start := time.Now()
		res, err := runMongo(ctx, pool, profile, req)
		return mongoConsoleMsg{tab: t, op: req.Operation, res: res, took: time.Since(start), err: err, cancelled: ctx.Err() != nil}
	})
}

func (t *mongoConsoleTab) Update(msg tea.KeyPressMsg, keys keyMap, pool *connPool) tea.Cmd {
	if cmd, ok := t.updateFront(msg, keys, t.gridModal(), func() tea.Cmd { return t.runCmd(pool) }); ok {
		return cmd
	}
	if t.hasDocs {
		if t.gridMode {
			var handled bool
			if t.grid, handled = t.grid.update(msg, keys); handled {
				return nil
			}
		} else if t.cards.update(msg, keys) {
			return nil
		}
	}
	switch {
	case key.Matches(msg, keys.EditBuffer):
		t.editing = true
	case key.Matches(msg, keys.ToggleView) && t.hasDocs:
		t.gridMode = !t.gridMode
	}
	return nil
}

// CopyText is the selected card field (or Document) or grid cell.
func (t *mongoConsoleTab) CopyText() (string, bool) {
	if !t.hasDocs {
		return "", false
	}
	if t.gridMode {
		return t.grid.cellCopyText()
	}
	return t.cards.copyText()
}

// CopyRowText is the selected Document as compact JSON.
func (t *mongoConsoleTab) CopyRowText() (string, bool) {
	if !t.hasDocs {
		return "", false
	}
	if t.gridMode {
		return t.grid.rowCopyText()
	}
	lines := t.cards.cardLines()
	if t.cards.cursor >= len(lines) {
		return "", false
	}
	return compactJSON(t.cards.docs[lines[t.cards.cursor].doc]), true
}

// ClickCell focuses the editor on a click in it, or moves the result
// cursor (a double-click toggles a nested field or opens the cell popup).
func (t *mongoConsoleTab) ClickCell(x, y, width, height int, double bool) {
	if t.gridModal() {
		return
	}
	top, ok := t.clickEditor(y, height)
	if !ok || !t.hasDocs {
		return
	}
	if t.gridMode {
		t.grid = clickGrid(t.grid, x, y, top, width, height, double)
		return
	}
	lines := t.cards.cardLines()
	i := scrollTopFor(t.cards.cursor, len(lines), height-top) + y - top
	if i >= len(lines) {
		return
	}
	t.cards.cursor = i
	if double {
		t.cards.toggleCursorLine()
	}
}

func (t *mongoConsoleTab) Help(keys keyMap) []key.Binding {
	if t.editing {
		return t.editorHelp(keys)
	}
	h := t.resultsHelpHead(keys)
	if t.hasDocs {
		if t.gridMode {
			h = append(h, keys.gridHelp()...)
		} else {
			h = append(h, keys.Up, keys.Down, keys.Expand)
		}
		h = append(h, keys.ToggleView)
	}
	return h
}

func (t *mongoConsoleTab) View(width, height int, focused bool) string {
	db := t.database
	if db == "" {
		db = "(profile default)"
	}
	status := t.statusLine(width, func() string {
		s := t.status
		if st := t.grid.searchStatus(); t.hasDocs && t.gridMode && st != "" {
			s += "  " + st
		}
		return s
	})
	var results func(int) string
	if t.showResults(t.hasDocs) {
		results = func(h int) string {
			if t.gridMode {
				return t.grid.view(width, h, focused && !t.editing)
			}
			return t.cards.cardsView(width, h, focused && !t.editing)
		}
	}
	return t.frame("mongodb console · db: "+db, status, width, height, focused, results)
}

func (t *mongoConsoleTab) cellPopupOpen() bool { return t.gridMode && t.grid.popupOpen() }
