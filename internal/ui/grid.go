package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// grid is the cell-addressable component shared by every tab kind that
// shows tabular results: a fixed header row, horizontal scrolling that
// always keeps the header aligned with the cells, and a cell cursor. It
// holds no database or Adapter knowledge of its own; a Data view hands it
// already-JSON-safe columns and rows (values.JSON has already turned
// binary data into a "b64:"-prefixed string by the time a row reaches the
// grid).
type grid struct {
	columns []string
	rows    [][]any

	cursorRow int
	cursorCol int

	// state is the popup and search state; see gridactions.go.
	state gridState
}

// nullLabel is the distinct text a NULL cell renders as, since nil must
// never look like an empty string.
const nullLabel = "<NULL>"

// cellText renders one JSON-safe cell value for display. NULL (nil) is
// shown as nullLabel; an empty string renders as nothing, which is what
// keeps it visually distinct from NULL.
func cellText(v any) string {
	if v == nil {
		return nullLabel
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// newGrid builds a grid at the origin cursor for columns and rows.
func newGrid(columns []string, rows [][]any) grid {
	return grid{columns: columns, rows: rows}
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// moveCursor shifts the cell cursor by (dRow, dCol), clamped to the loaded
// rows and columns; a grid with no rows or no columns never moves on that
// axis.
func (g grid) moveCursor(dRow, dCol int) grid {
	if len(g.rows) > 0 {
		g.cursorRow = clampInt(g.cursorRow+dRow, 0, len(g.rows)-1)
	}
	if len(g.columns) > 0 {
		g.cursorCol = clampInt(g.cursorCol+dCol, 0, len(g.columns)-1)
	}
	return g
}

const (
	columnSeparator = " "
	minColumnWidth  = 4
	maxColumnWidth  = 24
)

// columnWidths sizes each column to its header or widest loaded value,
// clamped to [minColumnWidth, maxColumnWidth] so one huge value cannot blow
// out the whole grid.
func (g grid) columnWidths() []int {
	widths := make([]int, len(g.columns))
	for i, c := range g.columns {
		widths[i] = len(c)
	}
	for _, row := range g.rows {
		for i, v := range row {
			if i >= len(widths) {
				break
			}
			if n := len(cellText(v)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for i := range widths {
		widths[i] = clampInt(widths[i], minColumnWidth, maxColumnWidth)
	}
	return widths
}

// visibleColumnRange returns the half-open [start, end) column indices
// that fit within width, always including cursorCol: it grows outward from
// the cursor so the header and every row slice the very same window, which
// is what keeps the header aligned with the cells while scrolling
// horizontally. The range is recomputed fresh on every render rather than
// stored, so there is no separate scroll state that could drift from the
// cursor.
func visibleColumnRange(widths []int, cursorCol, width int) (int, int) {
	if len(widths) == 0 {
		return 0, 0
	}
	cursorCol = clampInt(cursorCol, 0, len(widths)-1)
	start, end := cursorCol, cursorCol+1
	used := widths[cursorCol]
	for {
		grew := false
		if start > 0 && used+1+widths[start-1] <= width {
			start--
			used += 1 + widths[start]
			grew = true
		}
		if end < len(widths) && used+1+widths[end] <= width {
			used += 1 + widths[end]
			end++
			grew = true
		}
		if !grew {
			break
		}
	}
	return start, end
}

var (
	gridHeaderStyle = lipgloss.NewStyle().Background(colorBarBg).Foreground(colorMuted)
	gridRowSelStyle = lipgloss.NewStyle().Background(colorSelBg).Foreground(colorBright)
	gridCursorStyle = lipgloss.NewStyle().Background(colorAccent).Foreground(colorOnBadge).Bold(true)
	gridNullStyle   = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	gridMatchStyle  = lipgloss.NewStyle().Foreground(colorAccent).Underline(true).Bold(true)
)

// renderCell truncates text to width and styles it on base (the row's
// style) as the cursor cell, a search match, or a NULL cell when
// applicable.
func renderCell(text string, width int, base lipgloss.Style, selected, focused, isNull, isMatch bool) string {
	if len(text) > width {
		text = text[:width]
	}
	style := base
	switch {
	case selected && focused:
		style = gridCursorStyle
	case isMatch:
		style = gridMatchStyle.Background(base.GetBackground())
	case isNull:
		style = gridNullStyle.Background(base.GetBackground())
	}
	return style.Width(width).Render(text)
}

// fillLine pads line with base's background to exactly width cells.
func fillLine(line string, width int, base lipgloss.Style) string {
	if gap := width - lipgloss.Width(line); gap > 0 {
		line += base.Render(strings.Repeat(" ", gap))
	}
	return line
}

// view renders the grid clipped to width x height: one fixed header row,
// then as many data rows as fit, scrolled vertically to keep the cursor
// row visible (reusing the tree's own scrollTopFor) and horizontally to
// keep the cursor column visible. The cursor row is highlighted across the
// full width and the cursor cell in the accent color while focused. An
// open cell popup is drawn centered over the grid.
func (g grid) view(width, height int, focused bool) string {
	if len(g.columns) == 0 {
		return mutedStyle.Render("No columns.")
	}
	widths := g.columnWidths()
	colStart, colEnd := visibleColumnRange(widths, g.cursorCol, width)

	sep := func(base lipgloss.Style) string { return base.Render(columnSeparator) }
	headerCells := make([]string, 0, colEnd-colStart)
	for i := colStart; i < colEnd; i++ {
		headerCells = append(headerCells, renderCell(g.columns[i], widths[i], gridHeaderStyle, false, false, false, false))
	}
	lines := []string{fillLine(strings.Join(headerCells, sep(gridHeaderStyle)), width, gridHeaderStyle)}

	bodyHeight := height - 1
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	if len(g.rows) == 0 {
		lines = append(lines, mutedStyle.Render("No rows."))
		return g.withPopup(strings.Join(lines, "\n"), width, height)
	}

	top := scrollTopFor(g.cursorRow, len(g.rows), bodyHeight)
	bottom := len(g.rows)
	if bodyHeight > 0 && top+bodyHeight < bottom {
		bottom = top + bodyHeight
	}
	for r := top; r < bottom; r++ {
		row := g.rows[r]
		base := lipgloss.NewStyle()
		if r == g.cursorRow {
			base = gridRowSelStyle
		}
		cells := make([]string, 0, colEnd-colStart)
		for c := colStart; c < colEnd; c++ {
			var v any
			if c < len(row) {
				v = row[c]
			}
			cells = append(cells, renderCell(cellText(v), widths[c], base, r == g.cursorRow && c == g.cursorCol, focused, v == nil, g.isMatch(r, c)))
		}
		line := strings.Join(cells, sep(base))
		if r == g.cursorRow {
			line = fillLine(line, width, base)
		}
		lines = append(lines, line)
	}
	return g.withPopup(strings.Join(lines, "\n"), width, height)
}

// withPopup returns body, or body with the cell popup composited over it
// while the popup is open.
func (g grid) withPopup(body string, width, height int) string {
	if !g.state.popup || width < 8 || height < 4 {
		return body
	}
	base := lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(body)
	return overlay(base, g.popupView(width, height), width, height)
}
