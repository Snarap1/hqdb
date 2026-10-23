package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/values"
)

// This file holds the grid's own interactive behaviors (ticket 09): the
// cell popup, cell and row copy text, and in-page search. They live on the
// shared grid rather than on the SQL Data view so every tab kind that
// embeds a grid (Data views now, Console results later) gets them for free.

// gridState is the grid's modal state: the popup and the search field.
type gridState struct {
	popup bool

	searching   bool   // the search field has keyboard focus
	searchInput string // text typed into the field so far
	query       string // the applied query; "" means no search
}

// popupOpen reports whether the cell popup is showing.
func (g grid) popupOpen() bool { return g.state.popup }

// searchEditing reports whether the search text field has keyboard focus.
func (g grid) searchEditing() bool { return g.state.searching }

// modal reports whether the grid currently owns every key (popup open or
// search field focused), so the embedding tab must not treat single
// letters as its own or as global keys.
func (g grid) modal() bool { return g.state.popup || g.state.searching }

// currentCell returns the value under the cell cursor.
func (g grid) currentCell() (any, bool) {
	if g.cursorRow >= len(g.rows) || g.cursorCol >= len(g.columns) {
		return nil, false
	}
	row := g.rows[g.cursorRow]
	if g.cursorCol >= len(row) {
		return nil, true
	}
	return row[g.cursorCol], true
}

// cellCopyText is what "y" copies: the current cell's value as plain text
// (a string verbatim, a number or boolean in its display form, a nested
// JSON value as compact JSON, NULL as an empty string). It reports false
// when the grid has no current cell.
func (g grid) cellCopyText() (string, bool) {
	v, ok := g.currentCell()
	if !ok {
		return "", false
	}
	switch v.(type) {
	case nil:
		return "", true
	case values.Doc, map[string]any, []any:
		b, err := json.Marshal(v)
		if err == nil {
			return string(b), true
		}
	}
	return cellText(v), true
}

// rowCopyText is what "Y" copies: the current row as one compact JSON
// object keyed by column name, in column order, with NULL as null. JSON is
// the documented row format because it is unambiguous for NULL, empty
// strings, and values containing separators.
func (g grid) rowCopyText() (string, bool) {
	if g.cursorRow >= len(g.rows) || len(g.columns) == 0 {
		return "", false
	}
	row := g.rows[g.cursorRow]
	var b bytes.Buffer
	b.WriteByte('{')
	for i, c := range g.columns {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(c)
		b.Write(k)
		b.WriteByte(':')
		var v any
		if i < len(row) {
			v = row[i]
		}
		val, err := json.Marshal(v)
		if err != nil {
			val, _ = json.Marshal(fmt.Sprint(v))
		}
		b.Write(val)
	}
	b.WriteByte('}')
	return b.String(), true
}

// popupText renders a value for the cell popup: JSON (a nested value, or a
// string holding a JSON object or array) pretty-printed with two-space
// indentation, NULL as nullLabel, anything else as its plain text.
func popupText(v any) string {
	switch t := v.(type) {
	case nil:
		return nullLabel
	case values.Doc, map[string]any, []any:
		if b, err := json.MarshalIndent(t, "", "  "); err == nil {
			return string(b)
		}
	case string:
		s := strings.TrimSpace(t)
		if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
			var out bytes.Buffer
			if json.Indent(&out, []byte(s), "", "  ") == nil {
				return out.String()
			}
		}
	}
	return cellText(v)
}

// isMatch reports whether the cell at (r, c) contains the applied search
// query, case-insensitively.
func (g grid) isMatch(r, c int) bool {
	if g.state.query == "" || r >= len(g.rows) || c >= len(g.rows[r]) {
		return false
	}
	return strings.Contains(strings.ToLower(cellText(g.rows[r][c])), strings.ToLower(g.state.query))
}

// matches lists every matching cell in row-major order.
func (g grid) matches() [][2]int {
	var out [][2]int
	for r, row := range g.rows {
		for c := range row {
			if c < len(g.columns) && g.isMatch(r, c) {
				out = append(out, [2]int{r, c})
			}
		}
	}
	return out
}

// jumpMatch moves the cursor to the next (dir > 0) or previous match
// relative to the cursor, wrapping around the page; with includeCurrent
// the cursor's own cell counts as the next match.
func (g grid) jumpMatch(dir int, includeCurrent bool) grid {
	ms := g.matches()
	if len(ms) == 0 {
		return g
	}
	cur := g.cursorRow*len(g.columns) + g.cursorCol
	pos := func(m [2]int) int { return m[0]*len(g.columns) + m[1] }
	pick := -1
	if dir > 0 {
		for i, m := range ms {
			if p := pos(m); p > cur || (includeCurrent && p == cur) {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = 0
		}
	} else {
		for i := len(ms) - 1; i >= 0; i-- {
			if pos(ms[i]) < cur {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = len(ms) - 1
		}
	}
	g.cursorRow, g.cursorCol = ms[pick][0], ms[pick][1]
	return g
}

// searchStatus renders the search state for the tab header: the field
// while typing, "search: q (i/n)" once applied (i is the cursor's match,
// 0 when the cursor is not on one), or "" with no search.
func (g grid) searchStatus() string {
	if g.state.searching {
		return "search: " + g.state.searchInput + "_"
	}
	if g.state.query == "" {
		return ""
	}
	ms := g.matches()
	if len(ms) == 0 {
		return "search: " + g.state.query + " (no matches)"
	}
	i := 0
	for n, m := range ms {
		if m[0] == g.cursorRow && m[1] == g.cursorCol {
			i = n + 1
		}
	}
	return fmt.Sprintf("search: %s (%d/%d)", g.state.query, i, len(ms))
}

// update handles one key for the grid: cursor movement, the popup, and
// search. It reports whether the key was the grid's, so the embedding tab
// can handle the rest itself.
func (g grid) update(k tea.KeyPressMsg, keys keyMap) (grid, bool) {
	if g.state.popup {
		if k.String() == "esc" || key.Matches(k, keys.CellPopup) {
			g.state.popup = false
		}
		return g, true
	}
	if g.state.searching {
		switch k.String() {
		case "esc":
			g.state.searching = false
		case "enter":
			g.state.searching = false
			g.state.query = g.state.searchInput
			g = g.jumpMatch(1, true)
		case "backspace":
			if r := []rune(g.state.searchInput); len(r) > 0 {
				g.state.searchInput = string(r[:len(r)-1])
			}
		default:
			switch {
			case key.Matches(k, keys.SearchNext):
				g.state.query = g.state.searchInput
				g = g.jumpMatch(1, false)
			case key.Matches(k, keys.SearchPrev):
				g.state.query = g.state.searchInput
				g = g.jumpMatch(-1, false)
			case k.Text != "":
				g.state.searchInput += k.Text
			}
		}
		return g, true
	}
	switch {
	case key.Matches(k, keys.Up):
		g = g.moveCursor(-1, 0)
	case key.Matches(k, keys.Down):
		g = g.moveCursor(1, 0)
	case key.Matches(k, keys.Left):
		g = g.moveCursor(0, -1)
	case key.Matches(k, keys.Right):
		g = g.moveCursor(0, 1)
	case key.Matches(k, keys.CellPopup):
		if _, ok := g.currentCell(); ok {
			g.state.popup = true
		}
	case key.Matches(k, keys.Search):
		g.state.searching = true
		g.state.searchInput = g.state.query
	case key.Matches(k, keys.SearchNext):
		g = g.jumpMatch(1, false)
	case key.Matches(k, keys.SearchPrev):
		g = g.jumpMatch(-1, false)
	default:
		return g, false
	}
	return g, true
}

// cellAt maps a click at (x, y), relative to the grid's own top-left
// corner as rendered by view(width, height, ...), to a (row, column) cell;
// ok is false for the header line, anything below the grid, or empty
// space.
func (g grid) cellAt(x, y, width, height int) (int, int, bool) {
	if y < 1 || y >= height || len(g.rows) == 0 || len(g.columns) == 0 {
		return 0, 0, false
	}
	bodyHeight := height - 1
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	r := scrollTopFor(g.cursorRow, len(g.rows), bodyHeight) + y - 1
	if r >= len(g.rows) {
		return 0, 0, false
	}
	widths := g.columnWidths()
	start, end := visibleColumnRange(widths, g.cursorCol, width)
	left := 0
	for c := start; c < end; c++ {
		if x >= left && x < left+widths[c] {
			return r, c, true
		}
		left += widths[c] + len(columnSeparator)
	}
	return 0, 0, false
}

// openPopupAt moves the cursor to (r, c) and opens its popup; it is what a
// double-click on a cell runs.
func (g grid) openPopupAt(r, c int) grid {
	g.cursorRow, g.cursorCol = r, c
	g.state.popup = true
	return g
}

var popupStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1)

// popupView renders the current cell's full value as a bordered box that
// fits within width x height: the column name and row as its title, the
// value with line numbers (JSON pretty-printed and highlighted), and the
// popup's keys at the bottom. A value taller than the box is cut off with
// a muted note of how many lines are hidden.
func (g grid) popupView(width, height int) string {
	v, _ := g.currentCell()
	inner := max(min(width-6, 96), 10)
	text := popupText(v)
	isJSON := text != cellText(v) || strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[")

	title := spread(inner, brightStyle.Render(g.columns[g.cursorCol]), mutedStyle.Render(fmt.Sprintf("row %d", g.cursorRow+1)))
	footer := keyHint(key.NewBinding(key.WithHelp("y", "copy"))) + "   " + keyHint(key.NewBinding(key.WithHelp("esc", "close")))

	src := strings.Split(text, "\n")
	shown := src
	if isJSON {
		shown = strings.Split(strings.TrimRight(highlightJSON(text), "\n"), "\n")
	}
	if v == nil {
		shown = []string{gridNullStyle.Render(nullLabel)}
	}
	gutterW := len(fmt.Sprint(len(src)))
	room := max(height-6, 1) // border, title, blank, blank, footer
	body := make([]string, 0, min(len(shown), room))
	textW := max(inner-gutterW-2, 1)
	gutter := lipgloss.NewStyle().Foreground(colorGutter).Width(gutterW).Align(lipgloss.Right)
	for i, l := range shown {
		if len(body) == room {
			break
		}
		if !isJSON && v != nil {
			// Plain text wraps instead of being cut at the box edge.
			// Plain text wraps instead of being cut at the box edge; only a
			// source line's first row carries its number.
			for j, w := range strings.Split(lipgloss.NewStyle().Width(textW).Render(l), "\n") {
				if len(body) == room {
					break
				}
				num := ""
				if j == 0 {
					num = fmt.Sprint(i + 1)
				}
				body = append(body, gutter.Render(num)+"  "+w)
			}
			continue
		}
		body = append(body, gutter.Render(fmt.Sprint(i+1))+"  "+lipgloss.NewStyle().Inline(true).MaxWidth(textW).Render(l))
	}
	if len(body) == room && len(shown) > room {
		body[room-1] = mutedStyle.Render(fmt.Sprintf("… %d more lines, y copies all", len(src)-room+1))
	}
	content := title + "\n\n" + strings.Join(body, "\n") + "\n\n" + footer
	return popupStyle.Width(inner + 4).Render(content)
}
