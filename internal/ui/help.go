package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// helpGroup is one titled column block of the keymap overlay.
type helpGroup struct {
	name     string
	bindings []key.Binding
}

// helpGroups lists every key by the area it acts in.
func helpGroups(k keyMap) []helpGroup {
	esc := func(desc string) key.Binding {
		return key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", desc))
	}
	return []helpGroup{
		{"GLOBAL", []key.Binding{k.FocusNext, k.FocusPrev, k.ToggleTree, k.Help, esc("leave text field"), k.Cancel, k.Quit}},
		{"TREE", []key.Binding{k.Up, k.Down, k.Expand, k.Filter, k.Refresh, k.Disconnect, k.Console, k.DDL}},
		{"TABS", []key.Binding{k.PrevTab, k.NextTab, k.CloseTab}},
		{"DATA VIEW", []key.Binding{k.NextPage, k.PrevPage, k.PageSize, k.Count, k.Where, k.OrderBy, k.Export, k.ToggleView}},
		{"GRID", []key.Binding{k.Up, k.Down, k.Left, k.Right, k.CellPopup, k.Copy, k.CopyRow, k.Search, k.SearchNext, k.SearchPrev}},
		{"CONSOLE", []key.Binding{k.RunStatement, esc("leave editor"), k.EditBuffer, k.NewLineBelow, k.WordLeft, k.WordRight, k.MoveLineUp, k.MoveLineDown, k.DupLine, k.HistoryPrev, k.HistoryNext}},
	}
}

var helpBoxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1)

// renderHelp renders the full keymap as a bordered box, groups laid out in
// three columns, sized to fit width x height; the caller composites it over
// the screen.
func renderHelp(width, height int, keys keyMap) string {
	inner := min(max(width-6, 20), 104)
	const gap = 3
	colW := (inner - 2*gap) / 3
	keyW := 11

	block := func(g helpGroup) string {
		lines := []string{accentStyle.Bold(true).Render(g.name), mutedStyle.Render(strings.Repeat("─", colW))}
		for _, b := range g.bindings {
			h := b.Help()
			k := brightStyle.Width(keyW).Render(h.Key)
			lines = append(lines, lipgloss.NewStyle().Inline(true).MaxWidth(colW).Render(k+lipgloss.NewStyle().Foreground(colorSubtle).Render(h.Desc)))
		}
		return lipgloss.NewStyle().Width(colW).Render(strings.Join(lines, "\n"))
	}
	groups := helpGroups(keys)
	cols := make([]string, 3)
	for i, g := range groups {
		c := i % 3
		if cols[c] != "" {
			cols[c] += "\n\n"
		}
		cols[c] += block(g)
	}
	spacer := strings.Repeat(" ", gap)
	body := lipgloss.JoinHorizontal(lipgloss.Top, cols[0], spacer, cols[1], spacer, cols[2])

	title := spread(inner, brightStyle.Render("Keys"), mutedStyle.Render("mouse: click to focus/select · double-click to open"))
	footer := accentStyle.Render("esc") + " " + mutedStyle.Render("close")
	content := title + "\n\n" + body + "\n\n" + footer
	return helpBoxStyle.MaxHeight(max(height, 3)).Render(content)
}
