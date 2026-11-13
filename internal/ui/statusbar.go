package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var statusBarStyle = lipgloss.NewStyle().Background(colorStatBg)

// statusBar is what the bottom line shows: the mode badge, the tree's
// name filter while one narrows it, the transient notice, the focused
// pane's own key hints, and the global hints.
type statusBar struct {
	mode, filter, notice string
	hints, global        []key.Binding
}

// hintText renders one binding as the status bar shows it, "" for a
// binding without help.
func hintText(b key.Binding) string {
	h := b.Help()
	if h.Key == "" {
		return ""
	}
	sp := statusBarStyle.Render(" ")
	return statusBarStyle.Foreground(colorAccent).Render(h.Key) + sp + statusBarStyle.Foreground(colorSubtle).Render(h.Desc)
}

// render lays the bar out exactly width cells wide. The global hints are
// reserved at the right end, so "? keys" and "q quit" survive a narrow
// terminal; the notice is cut to the room left, and pane hints that do
// not fit after it are dropped whole rather than cut mid-word.
func (s statusBar) render(width int) string {
	sp := statusBarStyle.Render(" ")
	gap := statusBarStyle.Render("  ")
	var right string
	for _, b := range s.global {
		if t := hintText(b); t != "" {
			right += gap + t
		}
	}
	right += sp
	line := sp + badgeStyle.Render(s.mode)
	if lipgloss.Width(line)+lipgloss.Width(right) > width {
		right = ""
	}
	room := width - lipgloss.Width(right)
	add := func(part string, cut bool) bool {
		next := gap + part
		w := lipgloss.Width(next)
		if lipgloss.Width(line)+w > room {
			if !cut || room-lipgloss.Width(line) < 8 {
				return false
			}
			next = ansi.Truncate(next, room-lipgloss.Width(line), "…")
		}
		line += next
		return true
	}
	if s.filter != "" {
		add(statusBarStyle.Foreground(colorAccent).Render("filter: "+s.filter), true)
	}
	if s.notice != "" {
		add(statusBarStyle.Foreground(colorBright).Bold(true).Render(singleLine(s.notice)), true)
	}
	for _, b := range s.hints {
		if t := hintText(b); t != "" && !add(t, false) {
			break
		}
	}
	line = ansi.Truncate(line, max(width, 0), "")
	if fill := width - lipgloss.Width(line) - lipgloss.Width(right); fill > 0 {
		line += statusBarStyle.Render(strings.Repeat(" ", fill))
	}
	return line + right
}
