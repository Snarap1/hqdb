package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// writeConfirm is a Console's pending write confirmation (ADR-0003: on a
// read-write Profile the TUI asks before a write; on a read-only one it
// refuses what the MCP server would refuse and asks before the rest).
// It is Console-kind agnostic: the SQL, Redis, and Mongo Consoles arm it
// from their own detectors with the same prompt and keys.
type writeConfirm struct {
	profile   string
	statement string
	run       func() tea.Cmd
}

// ask arms the confirmation for statement on profile; run starts it and
// is called only on "y".
func (c *writeConfirm) ask(profile, statement string, run func() tea.Cmd) {
	c.profile, c.statement, c.run = profile, statement, run
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

// prompt is the question, fitted to width: a long statement is collapsed
// to one line and shortened with "..." so "Run? y/N" always shows.
func (c *writeConfirm) prompt(width int) string {
	head := "This will write to " + c.profile + ": "
	const tail = ". Run? y/N"
	stmt := []rune(strings.Join(strings.Fields(c.statement), " "))
	if room := width - len([]rune(head)) - len(tail); len(stmt) > room {
		stmt = append(stmt[:max(0, room-3)], []rune("...")...)
	}
	return head + string(stmt) + tail
}
