package ui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// grid is the cell-addressable component shared by every tab kind that
// shows tabular results: a fixed header row, horizontal scrolling that
// always keeps the header aligned with the cells, and a cell cursor. It
// holds no database or Adapter knowledge of its own; a Data view hands it
// already-JSON-safe columns and rows (values.JSON has already turned
// binary data into a "b64:"-prefixed string by the time a row reaches the
// grid).
//
// Every grid comes from newGrid, and its columns and rows are never
// changed after: that is what lets newGrid lay every cell out once
// (cells) instead of on every frame.
type grid struct {
	columns []string
	rows    [][]any

	// cells is the laid-out header and rows, shared by every copy of the
	// grid; nil only on the zero grid, which has no columns.
	cells *gridCells

	cursorRow int
	cursorCol int

	// state is the popup and search state; see gridactions.go.
	state gridState
}

// gridCells is a grid's cells as a frame draws them, computed once by
// newGrid.
type gridCells struct {
	// widths is each column's width in terminal cells (columnWidths).
	widths []int
	header []gridCell
	// rows holds one cell per column for every row; a row shorter than
	// the columns is padded with NULL cells.
	rows [][]gridCell

	// matchQuery and matchList memoize matches for the last search query.
	matchQuery string
	matchList  [][2]int

	// bodyRows is how many rows the last frame showed, for PgUp and PgDn.
	bodyRows int
}

// gridCell is one laid-out cell: its display text fitted to the column,
// the padding that fills the rest of the column, and what search and the
// styles need to know about it.
type gridCell struct {
	// text is the display text, truncated with an ellipsis to the
	// column's width; pad is the number of spaces that fill the column.
	text string
	pad  int
	// lower is the cell's full text, lowercased, for search; "" for a
	// cell a short row lacks, which never matches.
	lower string
	null  bool
	// right is set for a number, which is right-aligned.
	right bool
}

// nullLabel is the distinct text a NULL cell renders as, since nil must
// never look like an empty string.
const nullLabel = "<NULL>"

// ellipsis ends a cell's text that is cut at its column's width.
const ellipsis = "…"

// cellText renders one JSON-safe cell value as text: what the cell popup
// and copy start from. NULL (nil) is nullLabel; an empty string is
// nothing, which is what keeps it visually distinct from NULL.
func cellText(v any) string {
	if v == nil {
		return nullLabel
	}
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		return strconv.FormatBool(t)
	}
	return fmt.Sprint(v)
}

// displayText is text as one line of a grid cell: a line break (\r\n,
// \n, or \r) shows as ⏎, a tab as a space, and any other control
// character or invalid UTF-8 as U+FFFD, so a value can neither break its
// row nor send an escape sequence to the terminal.
func displayText(text string) string {
	if isDisplaySafe(text) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case r == '\r' && strings.HasPrefix(text[i+1:], "\n"):
			b.WriteRune('⏎')
			size = 2
		case r == '\n' || r == '\r':
			b.WriteRune('⏎')
		case r == '\t':
			b.WriteByte(' ')
		case r == utf8.RuneError && size == 1, unicode.IsControl(r):
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteString(text[i : i+size])
		}
		i += size
	}
	return b.String()
}

// isDisplaySafe reports whether text has no control character and is
// valid UTF-8, the common case displayText returns unchanged.
func isDisplaySafe(text string) bool {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c < 0x20 || c == 0x7f {
			return false
		}
		if c >= 0x80 {
			// A C1 control character is U+0080..U+009F, encoded as 0xC2
			// followed by 0x80..0x9F.
			if c == 0xc2 && i+1 < len(text) && text[i+1] < 0xa0 {
				return false
			}
		}
	}
	return utf8.ValidString(text)
}

// isNumber reports whether a JSON-safe cell value is a number, which the
// grid right-aligns.
func isNumber(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return true
	}
	return false
}

// newGrid builds a grid at the origin cursor for columns and rows, laying
// every cell out once: its display text, display width, column width,
// truncation, and lowercase search text.
func newGrid(columns []string, rows [][]any) grid {
	return grid{columns: columns, rows: rows, cells: layoutCells(columns, rows)}
}

// cellDraft is a cell's display text before its column's width is known:
// its width in terminal cells, and whether it is printable ASCII (one
// cell per byte), which measures and cuts without grapheme segmentation.
type cellDraft struct {
	text  string
	width int
	ascii bool
}

// measureLimit is how many bytes of a non-ASCII value are measured first:
// far more than maxColumnWidth cells of anything but zero-width runes, so
// a huge value is not segmented whole only to be cut to one column.
const measureLimit = 1024

// draftOf is text's display text and width. A value longer than
// measureLimit keeps only its head when that alone is wider than any
// column, since the rest is cut off anyway.
func draftOf(text string) cellDraft {
	if isPrintableASCII(text) {
		return cellDraft{text, len(text), true}
	}
	if len(text) > measureLimit {
		cut := measureLimit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		head := displayText(text[:cut])
		if w := ansi.StringWidth(head); w > maxColumnWidth {
			return cellDraft{head, w, false}
		}
	}
	t := displayText(text)
	return cellDraft{t, ansi.StringWidth(t), false}
}

// isPrintableASCII reports whether text is only bytes 0x20..0x7E.
func isPrintableASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if c := text[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func layoutCells(columns []string, rows [][]any) *gridCells {
	n := len(columns)
	c := &gridCells{widths: make([]int, n), header: make([]gridCell, n), rows: make([][]gridCell, len(rows))}
	header := make([]cellDraft, n)
	for i, name := range columns {
		header[i] = draftOf(name)
		c.widths[i] = header[i].width
	}
	// One backing array for every row's drafts and cells keeps a large
	// page to a few allocations.
	drafts := make([]cellDraft, len(rows)*n)
	all := make([]gridCell, len(rows)*n)
	for r, row := range rows {
		cells := all[r*n : (r+1)*n : (r+1)*n]
		for i := range cells {
			var v any
			if i < len(row) {
				v = row[i]
			}
			text := cellText(v)
			if i < len(row) {
				cells[i].lower = strings.ToLower(text)
			}
			d := draftOf(text)
			drafts[r*n+i] = d
			cells[i].null, cells[i].right = v == nil, isNumber(v)
			if d.width > c.widths[i] {
				c.widths[i] = d.width
			}
		}
		c.rows[r] = cells
	}
	for i := range c.widths {
		c.widths[i] = clampInt(c.widths[i], minColumnWidth, maxColumnWidth)
		c.header[i].fit(header[i], c.widths[i])
	}
	for r, cells := range c.rows {
		for i := range cells {
			cells[i].fit(drafts[r*n+i], c.widths[i])
		}
	}
	return c
}

// fit sets the cell's text to d cut to width with an ellipsis, and its
// padding to what is left of width.
func (c *gridCell) fit(d cellDraft, width int) {
	c.text, c.pad = d.text, width-d.width
	switch {
	case d.width <= width:
	case d.ascii:
		// The ellipsis is one cell wide.
		c.text, c.pad = d.text[:width-1]+ellipsis, 0
	default:
		c.text = ansi.Truncate(d.text, width, ellipsis)
		c.pad = width - ansi.StringWidth(c.text)
	}
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

// columnWidths is each column's width in terminal cells: its header or
// widest loaded value by display width, clamped to [minColumnWidth,
// maxColumnWidth] so one huge value cannot blow out the whole grid.
func (g grid) columnWidths() []int {
	if g.cells == nil {
		return nil
	}
	return g.cells.widths
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

// cellPaint is a style's escape sequences, rendered once through lipgloss
// so a frame writes them around each cell instead of rendering every cell
// through lipgloss: the text carries the whole style and the padding only
// its background, as lipgloss pads a cell of a given Width.
type cellPaint struct {
	pre, suf       string
	padPre, padSuf string
}

// paintOf derives a cellPaint from s.
func paintOf(s lipgloss.Style) cellPaint {
	var p cellPaint
	p.pre, p.suf = styleAround(s)
	p.padPre, p.padSuf = styleAround(lipgloss.NewStyle().Background(s.GetBackground()))
	return p
}

// styleAround is what s renders before and after a text.
func styleAround(s lipgloss.Style) (pre, suf string) {
	out := s.Render("\x00")
	i := strings.IndexByte(out, 0)
	if i < 0 {
		return "", ""
	}
	return out[:i], out[i+1:]
}

// gridPaints are the grid's styles as cellPaints; null and match are on
// a plain row ([0]) and on the cursor row ([1]).
var gridPaints = struct {
	header, plain, selected, cursor cellPaint
	null, match                     [2]cellPaint
}{
	header:   paintOf(gridHeaderStyle),
	plain:    paintOf(lipgloss.NewStyle()),
	selected: paintOf(gridRowSelStyle),
	cursor:   paintOf(gridCursorStyle),
	null:     [2]cellPaint{paintOf(gridNullStyle), paintOf(gridNullStyle.Background(colorSelBg))},
	match:    [2]cellPaint{paintOf(gridMatchStyle), paintOf(gridMatchStyle.Background(colorSelBg))},
}

// blanks is the run of spaces padding is cut from.
var blanks = strings.Repeat(" ", maxColumnWidth)

// writeSpaces writes n spaces painted as padding.
func (p cellPaint) writeSpaces(b *strings.Builder, n int) {
	if n <= 0 {
		return
	}
	b.WriteString(p.padPre)
	for n > len(blanks) {
		b.WriteString(blanks)
		n -= len(blanks)
	}
	b.WriteString(blanks[:n])
	b.WriteString(p.padSuf)
}

// write writes c in p: its text, padded to the column's width on the
// right, or on the left for a number.
func (p cellPaint) write(b *strings.Builder, c *gridCell) {
	if c.right {
		p.writeSpaces(b, c.pad)
	}
	if c.text != "" {
		b.WriteString(p.pre)
		b.WriteString(c.text)
		b.WriteString(p.suf)
	}
	if !c.right {
		p.writeSpaces(b, c.pad)
	}
}

// writeSeparator writes the column separator in p's row style.
func (p cellPaint) writeSeparator(b *strings.Builder) {
	b.WriteString(p.pre)
	b.WriteString(columnSeparator)
	b.WriteString(p.suf)
}

// view renders the grid clipped to width x height: one fixed header row,
// then as many data rows as fit, scrolled vertically to keep the cursor
// row visible (reusing the tree's own scrollTopFor) and horizontally to
// keep the cursor column visible. The cursor row is highlighted across the
// full width and the cursor cell in the accent color while focused. An
// open cell popup is drawn centered over the grid.
func (g grid) view(width, height int, focused bool) string {
	if len(g.columns) == 0 || g.cells == nil {
		return mutedStyle.Render("No columns.")
	}
	c := g.cells
	widths := c.widths
	colStart, colEnd := visibleColumnRange(widths, g.cursorCol, width)
	used := colEnd - colStart - 1
	for i := colStart; i < colEnd; i++ {
		used += widths[i]
	}

	var b strings.Builder
	bodyHeight := max(height-1, 0)
	c.bodyRows = bodyHeight
	b.Grow((min(bodyHeight, len(c.rows)) + 1) * (used + 16*(colEnd-colStart)))
	p := gridPaints.header
	for i := colStart; i < colEnd; i++ {
		if i > colStart {
			p.writeSeparator(&b)
		}
		p.write(&b, &c.header[i])
	}
	p.writeSpaces(&b, width-used)

	if len(g.rows) == 0 {
		b.WriteByte('\n')
		b.WriteString(mutedStyle.Render("No rows."))
		return g.withPopup(b.String(), width, height)
	}

	query := strings.ToLower(g.state.query)
	top := scrollTopFor(g.cursorRow, len(g.rows), bodyHeight)
	bottom := len(g.rows)
	if bodyHeight > 0 && top+bodyHeight < bottom {
		bottom = top + bodyHeight
	}
	for r := top; r < bottom; r++ {
		b.WriteByte('\n')
		base, on := gridPaints.plain, 0
		if r == g.cursorRow {
			base, on = gridPaints.selected, 1
		}
		cells := c.rows[r]
		for col := colStart; col < colEnd; col++ {
			if col > colStart {
				base.writeSeparator(&b)
			}
			cell := &cells[col]
			p := base
			switch {
			case r == g.cursorRow && col == g.cursorCol && focused:
				p = gridPaints.cursor
			case query != "" && strings.Contains(cell.lower, query):
				p = gridPaints.match[on]
			case cell.null:
				p = gridPaints.null[on]
			}
			p.write(&b, cell)
		}
		if r == g.cursorRow {
			base.writeSpaces(&b, width-used)
		}
	}
	return g.withPopup(b.String(), width, height)
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
