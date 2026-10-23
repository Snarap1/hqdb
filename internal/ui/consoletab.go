package ui

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/console"
	"github.com/Snarap1/hqdb/internal/consolestate"
	"github.com/Snarap1/hqdb/internal/export"
	"github.com/Snarap1/hqdb/internal/readonly"
)

// consoleSession is a SQL Console's one pinned database session (spec:
// "each SQL Console pins one dedicated session for its whole life"): its
// own connection, never shared with the pool, holding one *sql.Conn. It
// opens lazily on the first statement and serializes statements, so two
// quick runs can never race onto two sessions.
type consoleSession struct {
	mu      sync.Mutex
	profile string
	// database is the postgres Database the session is bound to; "" for
	// the Profile's own until the session opens and ensure learns it.
	database string
	db       adapters.DB
	conn     *sql.Conn
	closed   bool
}

// ensure opens the pinned session if it is not open yet; the caller holds
// s.mu.
func (s *consoleSession) ensure(ctx context.Context, pool *connPool) error {
	if s.closed {
		return fmt.Errorf("console is closed")
	}
	if s.conn != nil {
		return nil
	}
	db, conn, err := s.connect(ctx, pool, s.database)
	if err != nil && s.database == "" && adapters.DefaultDatabaseUnusable(err) {
		// A discovering postgres Profile whose login has no usable default
		// Database: fall back to `postgres`, as discovery does (ADR-0004).
		// A Profile with dbname refuses it there, keeping the first error.
		if db2, conn2, err2 := s.connect(ctx, pool, "postgres"); err2 == nil {
			db, conn, err, s.database = db2, conn2, nil, "postgres"
		}
	}
	if err != nil {
		return err
	}
	s.db, s.conn = db, conn
	if s.database == "" && db.Kind() == "postgres" {
		// A Console on a discovering Profile's own node is bound to the
		// login's default Database; learn which, for the tab title. A
		// failure leaves the title unqualified.
		var name string
		if conn.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name) == nil {
			s.database = name
		}
	}
	return nil
}

// connect opens a dedicated connection to database of the session's
// Profile and pins one *sql.Conn on it.
func (s *consoleSession) connect(ctx context.Context, pool *connPool, database string) (adapters.DB, *sql.Conn, error) {
	db, err := pool.OpenDedicated(poolKey{s.profile, database})
	if err != nil {
		return nil, nil, err
	}
	sqlDB, ok := db.(*adapters.SQL)
	if !ok {
		db.Close()
		return nil, nil, adapters.ErrNotSQL(db.Kind(), "console")
	}
	conn, err := sqlDB.SQL().Conn(ctx)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, conn, nil
}

// boundDatabase is the Database the session is bound to, "" when unknown.
func (s *consoleSession) boundDatabase() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.database
}

// sessionResetNotice follows a cancellation that dropped the pinned
// session (spec: "Ctrl+C in a SQL Console resets its session").
const sessionResetNotice = "session reset (SET values and temp tables are lost)"

// run executes stmt on the pinned session, opening it first if needed;
// reset reports that a cancellation dropped the session.
func (s *consoleSession) run(ctx context.Context, pool *connPool, stmt string) (res *console.Result, reset bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(ctx, pool); err != nil {
		return nil, false, err
	}
	defer func() { reset = s.dropIfCancelled(ctx) }()
	res, err = console.Run(ctx, s.conn, stmt, console.PreviewLimit)
	return res, false, err
}

// dropIfCancelled discards the session after cancelled work: a driver may
// leave a connection whose statement was interrupted unusable, so the
// next statement opens a fresh session instead of failing. Session state
// (SET values, temporary tables) does not survive a cancellation, which
// the Console reports with sessionResetNotice. It reports whether it
// dropped the session. The caller holds s.mu.
func (s *consoleSession) dropIfCancelled(ctx context.Context) bool {
	if ctx.Err() == nil || s.conn == nil {
		return false
	}
	_ = s.conn.Close()
	s.db.Close()
	s.conn, s.db = nil, nil
	return true
}

// export reruns stmt on the pinned session, streaming every row (no
// preview limit) into destPath through the same core export.Query the MCP
// export_query tool uses; stmt already passed runCmd's screening.
func (s *consoleSession) export(ctx context.Context, pool *connPool, stmt, destPath, format string) (n int, reset bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(ctx, pool); err != nil {
		return 0, false, err
	}
	defer func() { reset = s.dropIfCancelled(ctx) }()
	n, err = export.Query(ctx, s.conn, stmt, destPath, format, 0)
	return n, false, err
}

// close releases the session; later runs fail instead of reopening.
func (s *consoleSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.conn != nil {
		_ = s.conn.Close()
		s.db.Close()
		s.conn, s.db = nil, nil
	}
}

// consoleTab is a SQL Console: an editor on top and the last statement's
// result below, bound to one Profile. It runs statements as typed, after
// runCmd has refused or confirmed a write by the Profile's access mode
// (ADR-0003).
type consoleTab struct {
	consoleCore
	session *consoleSession

	hasGrid bool
	grid    grid

	// lastStmt is the statement whose rows the grid shows, which is what
	// export reruns.
	lastStmt string
	// exporting is true while the export destination field has focus;
	// exportInput is its text, exportStatus the last export's outcome.
	exporting    bool
	exportInput  string
	exportStatus string
}

func newConsoleTab(profile, database string, state *consolestate.Store) *consoleTab {
	t := &consoleTab{session: &consoleSession{profile: profile, database: database}}
	t.init(profile, "SQL", state)
	t.database = database
	return t
}

func (t *consoleTab) EditingText() bool {
	return t.editingText(t.grid.modal()) || t.exporting
}

// Close releases the Console's pinned session when its tab closes.
func (t *consoleTab) Close() { t.session.close() }

// consoleMsg reports one statement's outcome to the Console that ran it.
type consoleMsg struct {
	tab  *consoleTab
	stmt string
	res  *console.Result
	err  error
	// cancelled is set when Ctrl+C stopped the statement, sessionReset
	// when that dropped the pinned session.
	cancelled    bool
	sessionReset bool
	// database is the Database the session ended up bound to.
	database string
}

// consoleExportMsg reports a Console export's outcome.
type consoleExportMsg struct {
	tab          *consoleTab
	format       string
	written      int
	err          error
	cancelled    bool
	sessionReset bool
}

// cancelledText is the status after a cancellation of what ("statement",
// "export"), naming the session reset when there was one.
func cancelledText(what string, sessionReset bool) string {
	if sessionReset {
		return what + " cancelled; " + sessionResetNotice
	}
	return what + " cancelled"
}

func applyConsoleExportMsg(msg consoleExportMsg) {
	if msg.cancelled {
		msg.tab.exportStatus = cancelledText("export", msg.sessionReset)
		return
	}
	if msg.err != nil {
		msg.tab.exportStatus = "export failed: " + msg.err.Error()
		return
	}
	msg.tab.exportStatus = fmt.Sprintf("export (%s): %d rows written", msg.format, msg.written)
}

func applyConsoleMsg(msg consoleMsg) {
	t := msg.tab
	t.exportStatus = ""
	if t.database == "" {
		t.database = msg.database
	}
	if msg.cancelled {
		// The previous result is gone with the interrupted statement's
		// session; only the cancellation is shown.
		t.hasGrid = false
	}
	if !t.finish(msg.err, msg.cancelled, cancelledText("statement", msg.sessionReset)) {
		return
	}
	if msg.res.HasRows {
		t.grid = newGrid(msg.res.Columns, msg.res.Rows)
		t.hasGrid = true
		t.lastStmt = msg.stmt
		t.status = fmt.Sprintf("%d rows in %s", len(msg.res.Rows), formatDuration(msg.res.Duration))
		if msg.res.Truncated {
			t.status += fmt.Sprintf(" - more rows exist, truncated at %d", console.PreviewLimit)
		}
		return
	}
	t.hasGrid = false
	if msg.res.Affected >= 0 {
		t.status = fmt.Sprintf("%d rows affected in %s", msg.res.Affected, formatDuration(msg.res.Duration))
	} else {
		t.status = "statement executed in " + formatDuration(msg.res.Duration)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(10 * time.Millisecond).String()
}

// runCmd runs the statement under the editor's cursor. A statement the
// MCP server would refuse (readonly.Violation) is refused on a read-only
// Profile and asks for confirmation on a read-write one (ADR-0003).
func (t *consoleTab) runCmd(pool *connPool) tea.Cmd {
	stmt := t.statementAt(console.StatementAt)
	if stmt == "" {
		return nil
	}
	return t.runOrAsk(stmt, readonly.Violation(stmt), func() tea.Cmd { return t.execCmd(pool, stmt) })
}

// execCmd runs stmt on the Console's session, unscreened.
func (t *consoleTab) execCmd(pool *connPool, stmt string) tea.Cmd {
	s := t.session
	return t.exec(stmt, func(ctx context.Context) tea.Msg {
		res, reset, err := s.run(ctx, pool, stmt)
		return consoleMsg{tab: t, stmt: stmt, res: res, err: err, cancelled: ctx.Err() != nil, sessionReset: reset, database: s.boundDatabase()}
	})
}

// exportCmd exports the grid's statement to destPath.
func (t *consoleTab) exportCmd(pool *connPool, destPath string) tea.Cmd {
	format, err := export.InferFormat(destPath)
	if err != nil {
		t.exportStatus = "export failed: " + err.Error()
		return nil
	}
	t.exportStatus = "exporting..."
	s, stmt := t.session, t.lastStmt
	ctx, done := t.work.start()
	return func() tea.Msg {
		defer done()
		n, reset, err := s.export(ctx, pool, stmt, destPath, format)
		return consoleExportMsg{tab: t, format: format, written: n, err: err, cancelled: ctx.Err() != nil, sessionReset: reset}
	}
}

func (t *consoleTab) updateExportField(msg tea.KeyPressMsg, pool *connPool) tea.Cmd {
	switch msg.String() {
	case "esc":
		t.exporting = false
	case "enter":
		t.exporting = false
		if dest := strings.TrimSpace(t.exportInput); dest != "" {
			return t.exportCmd(pool, dest)
		}
	case "backspace":
		if r := []rune(t.exportInput); len(r) > 0 {
			t.exportInput = string(r[:len(r)-1])
		}
	default:
		t.exportInput += msg.Text
	}
	return nil
}

func (t *consoleTab) Update(msg tea.KeyPressMsg, keys keyMap, pool *connPool) tea.Cmd {
	if t.exporting && !t.confirm.pending() {
		return t.updateExportField(msg, pool)
	}
	if cmd, ok := t.updateFront(msg, keys, t.grid.modal(), func() tea.Cmd { return t.runCmd(pool) }); ok {
		return cmd
	}
	if t.hasGrid {
		var handled bool
		if t.grid, handled = t.grid.update(msg, keys); handled {
			return nil
		}
	}
	switch {
	case key.Matches(msg, keys.EditBuffer):
		t.editing = true
	case key.Matches(msg, keys.Export) && t.hasGrid:
		t.exporting, t.exportInput = true, ""
	}
	return nil
}

// CopyText is the current result cell, for the global copy key.
func (t *consoleTab) CopyText() (string, bool) {
	if !t.hasGrid {
		return "", false
	}
	return t.grid.cellCopyText()
}

// CopyRowText is the current result row as JSON, for the row copy key.
func (t *consoleTab) CopyRowText() (string, bool) {
	if !t.hasGrid {
		return "", false
	}
	return t.grid.rowCopyText()
}

// consoleLayout splits the content height into the editor's height and the
// grid's top offset (header line, editor, separator, status line).
func consoleLayout(height int) (editorHeight, gridTop int) {
	editorHeight = max(3, (height-3)*2/5)
	return editorHeight, editorHeight + 3
}

// clickGrid moves g's cursor to the clicked cell below top, or opens its
// popup on a double-click; the Consoles with a grid share it.
func clickGrid(g grid, x, y, top, width, height int, double bool) grid {
	r, c, ok := g.cellAt(x, y-top, width, height-top)
	switch {
	case !ok:
	case double:
		g = g.openPopupAt(r, c)
	default:
		g.cursorRow, g.cursorCol = r, c
	}
	return g
}

// ClickCell moves the result grid's cursor, or opens its popup on a
// double-click; a click in the editor focuses the editor.
func (t *consoleTab) ClickCell(x, y, width, height int, double bool) {
	if t.grid.modal() {
		return
	}
	if top, ok := t.clickEditor(y, height); ok && t.hasGrid {
		t.grid = clickGrid(t.grid, x, y, top, width, height, double)
	}
}

func (t *consoleTab) Help(keys keyMap) []key.Binding {
	if t.editing {
		return t.editorHelp(keys)
	}
	h := t.resultsHelpHead(keys)
	if t.hasGrid {
		h = append(append(h, keys.gridHelp()...), keys.Export)
	}
	return h
}

var consoleRuleStyle = lipgloss.NewStyle().Foreground(colorBorder)

func (t *consoleTab) View(width, height int, focused bool) string {
	status := t.statusLine(width, func() string {
		s := t.status
		if st := t.grid.searchStatus(); t.hasGrid && st != "" {
			s += "  " + st
		}
		if t.exportStatus != "" {
			s += "  " + t.exportStatus
		}
		return s
	})
	if t.exporting && !t.confirm.pending() {
		status = fieldText(width, "e", "export to (.csv/.json/.ndjson)", t.exportInput, true)
	}
	var results func(int) string
	if t.showResults(t.hasGrid) {
		results = func(h int) string { return t.grid.view(width, h, focused && !t.editing) }
	}
	return t.frame("SQL console · own session · autocommit", status, width, height, focused, results)
}

func (t *consoleTab) cellPopupOpen() bool { return t.hasGrid && t.grid.popupOpen() }
