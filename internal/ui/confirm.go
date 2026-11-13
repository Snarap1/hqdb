package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// writeConfirm is a Console's pending write confirmation (ADR-0003: on a
// read-write Profile the TUI asks before a write; on a read-only one it
// refuses what the MCP server would refuse and asks before the rest).
// It is Console-kind agnostic: the SQL, Redis, and Mongo Consoles arm it
// from their own detectors with the same prompt and keys.
type writeConfirm struct {
	profile   string
	statement string
	// reason is why the statement counts as a write: the detector's
	// violation (readonly.Violation, rediscmd.CommandViolation,
	// mongocmd.OperationViolation or PipelineViolation).
	reason string
	run    func() tea.Cmd
}

// ask arms the confirmation for statement on profile, which the detector
// flagged for reason; run starts it and is called only on "y".
func (c *writeConfirm) ask(profile, statement, reason string, run func() tea.Cmd) {
	c.profile, c.statement, c.reason, c.run = profile, statement, reason, run
}

func (c *writeConfirm) pending() bool { return c.run != nil }

// answer consumes the key that answers the prompt: "y" returns the run
// command, any other key cancels. cancelled reports the latter.
func (c *writeConfirm) answer(msg tea.KeyPressMsg) (run tea.Cmd, cancelled bool) {
	start := c.run
	*c = writeConfirm{}
	if msg.String() == "y" {
		return start(), false
	}
	return nil, true
}

// confirmTail ends the prompt's first line, so the question always shows.
const confirmTail = " Run? y/N"

// prompt is the question, fitted to width: the statement collapsed to one
// line and, when too long, cut with an ellipsis so "Run? y/N" always shows;
// then, on a second line, why the statement counts as a write.
func (c *writeConfirm) prompt(width int) []string {
	head := "This will write to " + c.profile + ": "
	stmt := strings.Join(strings.Fields(c.statement), " ")
	if room := width - ansi.StringWidth(head) - ansi.StringWidth(confirmTail); ansi.StringWidth(stmt) > room {
		stmt = ansi.Truncate(stmt, max(room, 1), "…")
	} else if !strings.HasSuffix(stmt, ".") {
		stmt += "."
	}
	lines := []string{head + stmt + confirmTail}
	if c.reason != "" {
		lines = append(lines, "why: "+c.reason)
	}
	return lines
}
