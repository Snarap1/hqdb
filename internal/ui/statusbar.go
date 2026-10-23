package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

var statusBarStyle = lipgloss.NewStyle().Background(colorStatBg)

// renderStatusBar renders the mode badge and then the key hints for
// whatever pane currently has focus, so the hints always match what the
// focused pane actually does. bindings is the focused pane's own hints
// followed by the global ones. filterQuery, when non-empty, is shown right
// after the badge so it is always clear a name filter is narrowing the
// tree. The bar is exactly width cells: hints that do not fit are dropped
// whole rather than cut mid-word.
func renderStatusBar(width int, mode string, bindings []key.Binding, filterQuery string) string {
	sp := statusBarStyle.Render(" ")
	parts := []string{badgeStyle.Render(mode)}
	if filterQuery != "" {
		parts = append(parts, statusBarStyle.Foreground(colorAccent).Render("filter: "+filterQuery))
	}
	for _, b := range bindings {
		h := b.Help()
		if h.Key == "" {
			continue
		}
		parts = append(parts, statusBarStyle.Foreground(colorAccent).Render(h.Key)+sp+statusBarStyle.Foreground(colorSubtle).Render(h.Desc))
	}
	line := sp
	for i, p := range parts {
		next := p
		if i > 0 {
			next = statusBarStyle.Render("  ") + p
		}
		if lipgloss.Width(line)+lipgloss.Width(next) > width {
			break
		}
		line += next
	}
	if gap := width - lipgloss.Width(line); gap > 0 {
		line += statusBarStyle.Render(strings.Repeat(" ", gap))
	}
	return line
}
