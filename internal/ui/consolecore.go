package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	// runAt is when the running statement started.
	runAt time.Time
	// confirm holds a write statement awaiting "y" before it runs.
	confirm writeConfirm
	// status is the result line; err is the last statement's error.
	status string
	err    error
	// errPopup shows err's full text in the cell popup (Enter on the
	// results while an error is shown).
	errPopup grid
	// statusRows is how many lines the status took in the last frame,
	// which the results area starts below.
	statusRows int

	// work tracks the running statement (and export) for Ctrl+C.
	work cancelSet
}

// maxStatusRows is how many lines an error or refusal wraps over in the
// status area before it is cut; the full text is in the error popup.
const maxStatusRows = 3

func (c *consoleCore) busy() bool { return c.running }

func (c *consoleCore) confirmPending() bool { return c.confirm.pending() }

// errPopupOpen reports whether the error popup is showing.
func (c *consoleCore) errPopupOpen() bool { return c.errPopup.popupOpen() }

// openErrPopup shows the current error's full text in the cell popup.
func (c *consoleCore) openErrPopup() {
	c.errPopup = newGrid([]string{"error"}, [][]any{{c.err.Error()}})
	c.errPopup.state.popupNote = c.profile
	c.errPopup = c.errPopup.openPopupAt(0, 0)
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
	return c.editing || c.confirm.pending() || resultsModal || c.errPopupOpen()
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
		return noticeCmd("refused: " + c.profile + " is a read-only profile")
	}
	if violation != "" {
		c.confirm.ask(c.profile, stmt, violation, run)
		return nil
	}
	return run()
}

// exec records stmt in the history, marks the Console running, and
// returns the tea.Cmd running fn under a cancellable context.
func (c *consoleCore) exec(stmt string, fn func(ctx context.Context) tea.Msg) tea.Cmd {
	c.running, c.runAt = true, time.Now()
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
	if c.errPopupOpen() {
		c.errPopup, _ = c.errPopup.update(msg, keys)
		return nil, true
	}
	if !c.editing && c.err != nil && !resultsModal && key.Matches(msg, keys.CellPopup) {
		c.openErrPopup()
		return nil, true
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
	_, top = consoleLayout(height, c.statusRows)
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
	return []key.Binding{keys.RunStatement, hint("esc", "results"), keys.NewLineBelow, keys.WordLeft, keys.WordRight, keys.MoveLineUp, keys.MoveLineDown, keys.DupLine, keys.HistoryPrev, keys.HistoryNext}
}

func (c *consoleCore) resultsHelpHead(keys keyMap) []key.Binding {
	h := []key.Binding{keys.RunStatement, keys.EditBuffer}
	if c.err != nil {
		h = append(h, hint(keys.CellPopup.Help().Key, "full error"))
	}
	return h
}

// help is the Console's status bar hints: the confirmation's keys while
// one is pending, the editor's while it has focus, else results (the
// Console kind's results hints after resultsHelpHead).
func (c *consoleCore) help(keys keyMap, results []key.Binding) []key.Binding {
	switch {
	case c.confirm.pending():
		return []key.Binding{hint("y", "run"), hint("any other key", "cancels")}
	case c.editing:
		return c.editorHelp(keys)
	}
	return append(c.resultsHelpHead(keys), results...)
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

// statusLines is the status area, one to maxStatusRows lines: the
// confirmation prompt with why the statement writes, the running state,
// the error (wrapped, and cut with a hint at the error popup when longer),
// or else idle (the Console's own status with its extras).
func (c *consoleCore) statusLines(width int, idle func() string) []string {
	switch {
	case c.confirm.pending():
		p := c.confirm.prompt(width)
		lines := []string{accentStyle.Bold(true).Render(p[0])}
		for _, l := range p[1:] {
			lines = append(lines, wrapLines(mutedStyle, l, width, maxStatusRows-1, "")...)
		}
		return lines
	case c.running:
		return []string{accentStyle.Render(busyText("Running", c.runAt)) + mutedStyle.Render(" · ctrl+c cancels")}
	case c.err != nil:
		more := " (enter: full error)"
		if c.editing {
			more = " (esc, enter: full error)"
		}
		return wrapLines(errorStyle, "Error: "+c.err.Error(), width, maxStatusRows, more)
	}
	return []string{lipgloss.NewStyle().Foreground(colorText).Render(idle())}
}

// wrapLines wraps text, sanitized for display, to width in style over at
// most n lines; when it needs more, the last line is cut to end with more.
func wrapLines(style lipgloss.Style, text string, width, n int, more string) []string {
	width = max(width, 1)
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(popupSafe(text)), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	if len(lines) > n {
		lines = lines[:n]
		last := ansi.Truncate(lines[n-1], max(width-ansi.StringWidth(more)-1, 1), "…")
		lines[n-1] = style.Render(last) + mutedStyle.Render(more)
		for i := range lines[:n-1] {
			lines[i] = style.Render(lines[i])
		}
		return lines
	}
	for i := range lines {
		lines[i] = style.Render(lines[i])
	}
	return lines
}

// showResults reports whether View should render the results area.
func (c *consoleCore) showResults(has bool) bool { return has && c.err == nil && !c.running }

// frame lays a Console out: header (the Profile, its access badge, sub,
// and the mode), editor, rule, status, and results (when results is
// non-nil, called with the area's height). The error popup, while open,
// is drawn over all of it.
func (c *consoleCore) frame(sub string, status []string, width, height int, focused bool, results func(h int) string) string {
	c.statusRows = len(status)
	editorHeight, top := consoleLayout(height, c.statusRows)
	clip := lipgloss.NewStyle().Inline(true).MaxWidth(max(width, 1))
	parts := []string{
		tabHeaderLine(width, c.access, c.profile, sub, c.mode()),
		c.editor.view(width, editorHeight, focused && c.editing),
		consoleRuleStyle.Render(strings.Repeat("─", max(0, width))),
	}
	for _, s := range status {
		parts = append(parts, clip.Render(s))
	}
	if results != nil {
		parts = append(parts, results(height-top))
	}
	out := strings.Join(parts, "\n")
	if c.errPopupOpen() {
		out = c.errPopup.withPopup(out, width, height)
	}
	return out
}

// accessText is the Console header's spelled-out access mode.
func (c *consoleCore) accessText() string {
	if c.access == config.ReadWrite {
		return string(config.ReadWrite)
	}
	return string(config.ReadOnly)
}
