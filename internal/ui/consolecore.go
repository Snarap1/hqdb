package ui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/consolestate"
)

// consoleCore is what every Console kind (SQL, mongodb, Redis) shares:
// the editor and its persisted history, the write confirmation, the
// running/status/error line, Ctrl+C cancellation, and the layout. Each
// Console embeds one and adds only how it parses, executes, and renders a
// statement.
type consoleCore struct {
	profile string
	// database is the postgres Database a SQL Console is bound to
	// (ADR-0004), shown in its title; "" for the Profile's own database.
	database string
	// access is the Profile's access mode; the zero value is read-only.
	access config.Access
	// tag names the Console kind in the tab bar (SQL, MONGO, REDIS).
	tag  string
	hist consoleHistory

	editor  editor
	editing bool // the editor, not the results, has keyboard focus

	running bool
	// confirm holds a write statement awaiting "y" before it runs.
	confirm writeConfirm
	// status is the result line; err is the last statement's error.
	status string
	err    error

	// work tracks the running statement (and export) for Ctrl+C.
	work cancelSet
}

// init binds c to profile and starts it in the editor, holding the
// Profile's saved buffer.
func (c *consoleCore) init(profile, tag string, state *consolestate.Store) {
	c.profile, c.tag, c.editing = profile, tag, true
	c.hist = newConsoleHistory(state, profile)
	c.editor = editor{}.insert(c.hist.restoredBuffer())
}

func (c *consoleCore) Title() string {
	if c.database != "" {
		return c.profile + ": " + c.database + " (Console)"
	}
	return c.profile + " (Console)"
}

func (c *consoleCore) Heading() tabHeading {
	object := "console"
	if c.database != "" {
		object = c.database + " console"
	}
	return tabHeading{tag: c.tag, profile: c.profile, object: object}
}

// Cancel cancels the Console's running work (Ctrl+C).
func (c *consoleCore) Cancel() { c.work.cancelAll() }

// editingText reports whether keys go to text input; resultsModal is the
// results' own modal state (a grid search or popup).
func (c *consoleCore) editingText(resultsModal bool) bool {
	return c.editing || c.confirm.pending() || resultsModal
}

// statementAt is the statement under the editor's cursor, as split picks it.
func (c *consoleCore) statementAt(split func(text string, cur int) string) string {
	return split(c.editor.text(), c.editor.cursorByte())
}

// setAccess binds the Console to its Profile's access mode.
func (c *consoleCore) setAccess(a config.Access) { c.access = a }

// runOrAsk runs stmt via run, refuses it, or first asks for the write
// confirmation (ADR-0003). violation is what the MCP server's detector
// reports for stmt, "" for a read: a read-only Profile refuses it like
// the MCP server does, and a read-write one asks before it runs.
func (c *consoleCore) runOrAsk(stmt, violation string, run func() tea.Cmd) tea.Cmd {
	if violation != "" && c.access != config.ReadWrite {
		c.err, c.status = fmt.Errorf("Refused (read-only profile): %s", violation), ""
		return nil
	}
	if violation != "" {
		c.confirm.ask(c.profile, stmt, run)
		return nil
	}
	return run()
}

// exec records stmt in the history, marks the Console running, and
// returns the tea.Cmd running fn under a cancellable context.
func (c *consoleCore) exec(stmt string, fn func(ctx context.Context) tea.Msg) tea.Cmd {
	c.running = true
	c.hist.record(stmt)
	ctx, done := c.work.start()
	return func() tea.Msg {
		defer done()
		return fn(ctx)
	}
}

// finish clears the running state for a statement's outcome and reports
// whether it produced a result to show: a cancellation shows cancelled
// as the status, and an error is shown by View.
func (c *consoleCore) finish(err error, cancelled bool, cancelledStatus string) bool {
	c.running = false
	c.err = err
	switch {
	case cancelled:
		c.err, c.status = nil, cancelledStatus
		return false
	case err != nil:
		c.status = ""
		return false
	}
	return true
}

// updateFront handles the keys every Console treats alike, in order: the
// confirmation's answer, the run key (unless resultsModal), and, while
// the editor has focus, Esc to leave it and editing or history keys. It
// reports whether it consumed msg.
func (c *consoleCore) updateFront(msg tea.KeyPressMsg, keys keyMap, resultsModal bool, run func() tea.Cmd) (tea.Cmd, bool) {
	if c.confirm.pending() {
		cmd, cancelled := c.confirm.answer(msg)
		if cancelled {
			c.err, c.status = nil, "Cancelled"
		}
		return cmd, true
	}
	if key.Matches(msg, keys.RunStatement) && !resultsModal {
		return run(), true
	}
	if c.editing {
		if msg.String() == "esc" {
			c.editing = false
		} else {
			c.editor = c.hist.editKey(c.editor, msg, keys)
		}
		return nil, true
	}
	return nil, false
}

// Paste inserts text into the editor while it has focus; a pending
// confirmation or the results area ignores it.
func (c *consoleCore) Paste(text string) {
	if c.confirm.pending() || !c.editing {
		return
	}
	c.editor = c.hist.paste(c.editor, text)
}

// clickEditor focuses the editor on a click in it and reports the results
// area's top when the click landed below it instead (ok true).
func (c *consoleCore) clickEditor(y, height int) (top int, ok bool) {
	_, top = consoleLayout(height)
	if y < top {
		c.editing = y > 0 && y < top-2
		return top, false
	}
	c.editing = false
	return top, true
}

// editorHelp is the help while the editor has focus; the rest of a
// Console's help starts with resultsHelpHead.
func (c *consoleCore) editorHelp(keys keyMap) []key.Binding {
	return []key.Binding{keys.RunStatement, keys.HistoryPrev, keys.HistoryNext}
}

func (c *consoleCore) resultsHelpHead(keys keyMap) []key.Binding {
	return []key.Binding{keys.RunStatement, keys.EditBuffer}
}

// mode is the header's editing/results label with the history position,
// in the accent color while the editor has the keys.
func (c *consoleCore) mode() string {
	m := mutedStyle.Render("results")
	if c.editing {
		m = accentStyle.Render("editing")
	}
	if p := c.hist.position(); p != "" {
		m += mutedStyle.Render(" · " + p)
	}
	return m
}

// statusLine is the confirmation prompt, the running state, the error, or
// else idle (the Console's own status with its extras).
func (c *consoleCore) statusLine(width int, idle func() string) string {
	switch {
	case c.confirm.pending():
		return accentStyle.Bold(true).Render(c.confirm.prompt(width))
	case c.running:
		return accentStyle.Render("Running...") + mutedStyle.Render("  ctrl+c cancels")
	case c.err != nil:
		return errorStyle.Render("Error: " + c.err.Error())
	}
	return lipgloss.NewStyle().Foreground(colorText).Render(idle())
}

// showResults reports whether View should render the results area.
func (c *consoleCore) showResults(has bool) bool { return has && c.err == nil && !c.running }

// frame lays a Console out: header (the Profile, sub, and the mode),
// editor, rule, status, and results (when results is non-nil, called with
// the area's height).
func (c *consoleCore) frame(sub, status string, width, height int, focused bool, results func(h int) string) string {
	editorHeight, top := consoleLayout(height)
	parts := []string{
		headerLine(width, c.profile, sub, c.mode()),
		c.editor.view(width, editorHeight, focused && c.editing),
		consoleRuleStyle.Render(strings.Repeat("─", max(0, width))),
		lipgloss.NewStyle().Inline(true).MaxWidth(max(width, 1)).Render(status),
	}
	if results != nil {
		parts = append(parts, results(height-top))
	}
	return strings.Join(parts, "\n")
}
