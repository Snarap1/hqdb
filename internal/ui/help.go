package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// helpGroup is one titled column block of the keymap overlay.
type helpGroup struct {
	name     string
	bindings []key.Binding
}

// helpGroups lists every key by the area it acts in.
func helpGroups(k keyMap) []helpGroup {
	move := hint("↑↓←→ hjkl", "move")
	paging := []key.Binding{k.PageUp, k.PageDown, k.Top, k.Bottom}
	return []helpGroup{
		{"GLOBAL", []key.Binding{k.FocusNext, k.FocusPrev, k.ToggleTree, k.Help, hint("esc", "leave text field"), k.Cancel, k.Quit, hint("mouse wheel", "scroll pane under pointer")}},
		{"TREE", append([]key.Binding{
			hint("↑↓ jk", "select"), hint("enter", "open Data view (Table, key)"), hint("enter", "expand/collapse (others)"),
			hint("→/l", "expand, Table columns"), k.TreeLeft, k.Filter, k.Refresh, k.Disconnect, k.Console, k.DDL,
		}, paging...)},
		{"TABS", []key.Binding{k.PrevTab, k.NextTab, k.JumpTab, k.CloseTab, hint("click", "select tab, ‹ N / N › hidden")}},
		{"DATA VIEW", []key.Binding{k.NextPage, k.PrevPage, k.PageSize, k.Count, k.Where, k.OrderBy, k.Export}},
		{"GRID", append([]key.Binding{move, k.CellPopup, k.Copy, k.CopyRow, k.Search, k.SearchNext, k.SearchPrev}, paging...)},
		{"MONGODB", []key.Binding{hint("f", "filter (shell syntax)"), hint("o", "sort"), k.ToggleView, hint("enter", "expand field (cards)"), hint("Y", "copy document (JSON)"), hint("use <db>", "switch Database (Console)")}},
		{"CONSOLE", []key.Binding{k.RunStatement, hint("esc", "leave editor"), k.EditBuffer, k.HistoryPrev, k.HistoryNext, hint("enter", "full error (results)"), hint("e", "export result (SQL)"), hint("y", "run a confirmed write")}},
		{"EDITOR", []key.Binding{k.NewLineBelow, k.WordLeft, k.WordRight, hint("ctrl+bksp", "delete word left"), hint("ctrl+del", "delete word right"), k.MoveLineUp, k.MoveLineDown, k.DupLine, hint("ctrl+u", "delete to line start"), hint("ctrl+k", "delete to line end"), hint("home/end", "line start/end"), hint("ctrl+home/end", "buffer start/end")}},
		{"REDIS", []key.Binding{hint("enter", "open key (tree)"), hint("alt+enter", "run line (Console)"), hint("write", "read-only refuses, read-write asks")}},
		{"DDL", []key.Binding{k.DDL, k.Copy, hint("↑↓ pgup/pgdn", "scroll")}},
	}
}

// orderedHelpGroups is helpGroups with the group named first (the focused
// pane's or tab's) moved to the front.
func orderedHelpGroups(k keyMap, first string) []helpGroup {
	groups := helpGroups(k)
	for i, g := range groups {
		if g.name == first && i > 0 {
			out := append([]helpGroup{g}, groups[:i]...)
			return append(out, groups[i+1:]...)
		}
	}
	return groups
}

var helpBoxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1)

// helpKeyWidth is the key column's width: the longest key plus a gap.
func helpKeyWidth(groups []helpGroup) int {
	w := 0
	for _, g := range groups {
		for _, b := range g.bindings {
			w = max(w, ansi.StringWidth(b.Help().Key))
		}
	}
	return w + 2
}

// renderHelp renders the full keymap as a bordered box sized to fit width
// x height, the composited caller's screen: the group named first leads,
// groups are laid out in as many columns as fit (up to three), and a
// keymap taller than the box scrolls from line scroll (clamped).
func renderHelp(width, height int, keys keyMap, first string, scroll int) string {
	groups := orderedHelpGroups(keys, first)
	inner := min(max(width-6, 20), 120)
	keyW := helpKeyWidth(groups)
	const gap = 3
	ncol := 3
	for ncol > 1 && (inner-(ncol-1)*gap)/ncol < keyW+18 {
		ncol--
	}
	colW := (inner - (ncol-1)*gap) / ncol
	keyStyle := brightStyle.Width(keyW)
	descStyle := lipgloss.NewStyle().Foreground(colorSubtle)
	clip := lipgloss.NewStyle().Inline(true).MaxWidth(colW)

	block := func(g helpGroup) string {
		lines := []string{accentStyle.Bold(true).Render(g.name), mutedStyle.Render(strings.Repeat("─", colW))}
		for _, b := range g.bindings {
			h := b.Help()
			lines = append(lines, clip.Render(keyStyle.Render(h.Key)+descStyle.Render(h.Desc)))
		}
		return lipgloss.NewStyle().Width(colW).Render(strings.Join(lines, "\n"))
	}
	cols := make([]string, ncol)
	for i, g := range groups {
		c := i % ncol
		if cols[c] != "" {
			cols[c] += "\n\n"
		}
		cols[c] += block(g)
	}
	parts := []string{cols[0]}
	for _, c := range cols[1:] {
		parts = append(parts, strings.Repeat(" ", gap), c)
	}
	body := strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, parts...), "\n")

	// Border, title, blank, blank, footer.
	room := max(height-6, 1)
	scroll = clampInt(scroll, 0, max(len(body)-room, 0))
	shown := body[scroll:min(scroll+room, len(body))]

	title := spread(inner, brightStyle.Render("Keys"), mutedStyle.Render("mouse: click to focus/select · double-click to open"))
	footer := accentStyle.Render("esc") + " " + mutedStyle.Render("close")
	if len(body) > room {
		footer = spread(inner, footer+"   "+accentStyle.Render("↑↓ pgup/pgdn g/G")+" "+mutedStyle.Render("scroll"),
			mutedStyle.Render(fmt.Sprintf("lines %d-%d of %d", scroll+1, scroll+len(shown), len(body))))
	}
	content := title + "\n\n" + strings.Join(shown, "\n") + "\n\n" + footer
	return helpBoxStyle.Render(content)
}
