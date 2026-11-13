package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// gridLines is g's frame at width x height without escape sequences, one
// string per line.
func gridLines(g grid, width, height int) []string {
	return strings.Split(ansi.Strip(g.view(width, height, false)), "\n")
}

func TestDisplayTextKeepsACellOnOneSafeLine(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain ascii", "hello world", "hello world"},
		{"cyrillic", "Привет, мир", "Привет, мир"},
		{"cjk and emoji", "日本語 👍", "日本語 👍"},
		{"newline", "line1\nline2", "line1⏎line2"},
		{"crlf is one break", "line1\r\nline2", "line1⏎line2"},
		{"lone carriage return", "a\rb", "a⏎b"},
		{"tab", "a\tb", "a b"},
		{"escape sequence", "\x1b[31mred", "�[31mred"},
		{"nul and del", "a\x00b\x7f", "a�b�"},
		{"c1 control", "a\u0085b", "a�b"},
		{"invalid utf-8", "a\xffb", "a�b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := displayText(c.in); got != c.want {
				t.Errorf("displayText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestColumnWidthsCountTerminalCellsNotBytes(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int
	}{
		{"cyrillic is one cell per letter", "Привет мир", 10},
		{"cjk is two cells per character", "日本語です", 10},
		{"emoji is two cells", "ok 👍👍👍", 9},
		{"combining accent adds no cell", "café au lait", 12},
		{"newline shows as one cell", "ab\ncd", 5},
		{"long cyrillic is clamped", strings.Repeat("щ", 40), maxColumnWidth},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGrid([]string{"v"}, [][]any{{c.value}})
			if got := g.columnWidths()[0]; got != c.want {
				t.Errorf("width = %d, want %d", got, c.want)
			}
		})
	}
}

func TestGridRowsStayAlignedWithWideAndMultilineValues(t *testing.T) {
	rows := [][]any{
		{"Привет", "日本語", "a\nb\nc"},
		{"ascii", "👍 ok", "tab\there"},
		{"ё", "中", "x\r\ny"},
	}
	g := newGrid([]string{"ru", "cjk", "multi"}, rows)
	w := g.columnWidths()
	total := w[0] + w[1] + w[2] + 2
	lines := gridLines(g, total, 10)
	if len(lines) != len(rows)+1 {
		t.Fatalf("frame has %d lines, want header + %d rows:\n%s", len(lines), len(rows), strings.Join(lines, "\n"))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != total {
			t.Errorf("line %d %q is %d cells wide, want %d", i, l, got, total)
		}
	}
	if !strings.Contains(lines[1], "a⏎b⏎c") || !strings.Contains(lines[2], "tab here") || !strings.Contains(lines[3], "x⏎y") {
		t.Errorf("multiline cells not flattened:\n%s", strings.Join(lines, "\n"))
	}
}

func TestGridTruncatesToTheColumnWithAnEllipsis(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string // the cell as shown, padded to the column
	}{
		{"ascii", strings.Repeat("a", 30), strings.Repeat("a", 23) + "…"},
		{"cyrillic", strings.Repeat("ж", 30), strings.Repeat("ж", 23) + "…"},
		// 11 two-cell characters and the ellipsis leave one cell of padding.
		{"cjk", strings.Repeat("語", 13), strings.Repeat("語", 11) + "… "},
		{"emoji", strings.Repeat("👍", 13), strings.Repeat("👍", 11) + "… "},
		{"flattened newlines", strings.Repeat("ab\n", 10), strings.Repeat("ab⏎", 7) + "ab…"},
		{"huge non-ascii value", strings.Repeat("я", 100000), strings.Repeat("я", 23) + "…"},
		{"fits exactly", strings.Repeat("ю", 24), strings.Repeat("ю", 24)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGrid([]string{"v"}, [][]any{{c.value}})
			lines := gridLines(g, maxColumnWidth, 5)
			got := lines[1]
			if got != c.want {
				t.Errorf("cell = %q, want %q", got, c.want)
			}
			if !utf8.ValidString(got) || ansi.StringWidth(got) != maxColumnWidth {
				t.Errorf("cell %q: valid UTF-8 %v, width %d, want %d", got, utf8.ValidString(got), ansi.StringWidth(got), maxColumnWidth)
			}
		})
	}
}

func TestGridTruncatesALongHeaderWithAnEllipsis(t *testing.T) {
	g := newGrid([]string{strings.Repeat("колонка_", 5)}, [][]any{{"x"}})
	if got, want := gridLines(g, maxColumnWidth, 5)[0], "колонка_колонка_колонка…"; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
}

func TestGridRightAlignsNumbers(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"integer", int64(7), "     7"},
		{"negative float", -1.5, "  -1.5"},
		{"unsigned", uint64(42), "    42"},
		{"numeric string stays left", "42", "42    "},
		{"boolean stays left", true, "true  "},
		{"null stays left", nil, nullLabel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGrid([]string{"amount"}, [][]any{{c.value}})
			if got := gridLines(g, 6, 5)[1]; got != c.want {
				t.Errorf("cell = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGridPopupAndCopyKeepTheRawValue(t *testing.T) {
	raw := "first line\n\tsecond line"
	g := newGrid([]string{"note"}, [][]any{{raw}})
	if got, _ := g.cellCopyText(); got != raw {
		t.Errorf("copy = %q, want the raw %q", got, raw)
	}
	if got := popupText(raw); got != raw {
		t.Errorf("popup text = %q, want the raw %q", got, raw)
	}
	if got := gridLines(g, 30, 5)[1]; !strings.HasPrefix(got, "first line⏎ second line") {
		t.Errorf("grid cell = %q, want the flattened text", got)
	}
}

func TestGridSearchesTheRawValue(t *testing.T) {
	g := newGrid([]string{"note"}, [][]any{{"alpha\nBeta"}, {"gamma"}})
	g.state.query = "a\nb"
	if ms := g.matches(); len(ms) != 1 || ms[0] != [2]int{0, 0} {
		t.Errorf("matches = %v, want the multiline cell", ms)
	}
	g.state.query = "AMM"
	if ms := g.matches(); len(ms) != 1 || ms[0] != [2]int{1, 0} {
		t.Errorf("matches for a new query = %v, want (1,0)", ms)
	}
}

func TestGridShortRowShowsWholeNullCells(t *testing.T) {
	g := newGrid([]string{"a", "b"}, [][]any{{"x", "y"}, {"z"}})
	if got := g.columnWidths()[1]; got != len(nullLabel) {
		t.Errorf("width of a column with a missing cell = %d, want %d", got, len(nullLabel))
	}
	if got := gridLines(g, 20, 5)[2]; !strings.Contains(got, nullLabel) {
		t.Errorf("short row = %q, want %s for the missing cell", got, nullLabel)
	}
}

func TestCellTextDistinguishesNullFromEmptyString(t *testing.T) {
	if got := cellText(nil); got != nullLabel {
		t.Errorf("cellText(nil) = %q, want %q", got, nullLabel)
	}
	if got := cellText(""); got != "" {
		t.Errorf(`cellText("") = %q, want ""`, got)
	}
	if nullLabel == "" {
		t.Fatal("nullLabel must not itself be empty, or NULL and \"\" would render identically")
	}
}

func TestCellTextPassesThroughOtherJSONSafeValues(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"b64:AAE=", "b64:AAE="},
		{int64(42), "42"},
		{1.5, "1.5"},
		{true, "true"},
	}
	for _, tc := range cases {
		if got := cellText(tc.in); got != tc.want {
			t.Errorf("cellText(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMoveCursorClampsToLoadedRowsAndColumns(t *testing.T) {
	g := newGrid([]string{"a", "b"}, [][]any{{1, 2}, {3, 4}, {5, 6}})
	g = g.moveCursor(-5, -5)
	if g.cursorRow != 0 || g.cursorCol != 0 {
		t.Errorf("underflow not clamped: row=%d col=%d", g.cursorRow, g.cursorCol)
	}
	g = g.moveCursor(100, 100)
	if g.cursorRow != 2 || g.cursorCol != 1 {
		t.Errorf("overflow not clamped: row=%d col=%d", g.cursorRow, g.cursorCol)
	}
}

func TestMoveCursorOnEmptyGridIsNoop(t *testing.T) {
	g := newGrid(nil, nil)
	g = g.moveCursor(3, 3)
	if g.cursorRow != 0 || g.cursorCol != 0 {
		t.Errorf("empty grid cursor moved: row=%d col=%d", g.cursorRow, g.cursorCol)
	}
}

func TestColumnWidthsSizeToHeaderOrWidestValueClamped(t *testing.T) {
	g := newGrid([]string{"id", "description"}, [][]any{
		{1, "short"},
		{2, nil},
	})
	widths := g.columnWidths()
	if widths[0] != minColumnWidth {
		t.Errorf("id column width = %d, want the minimum %d", widths[0], minColumnWidth)
	}
	if widths[1] != len("description") {
		t.Errorf("description column width = %d, want %d", widths[1], len("description"))
	}

	g2 := newGrid([]string{"x"}, [][]any{{string(make([]byte, maxColumnWidth*2))}})
	if got := g2.columnWidths()[0]; got != maxColumnWidth {
		t.Errorf("huge value width = %d, want clamped to %d", got, maxColumnWidth)
	}
}

func TestVisibleColumnRangeAlwaysIncludesCursorAndFitsWidth(t *testing.T) {
	widths := []int{5, 5, 5, 5, 5}
	start, end := visibleColumnRange(widths, 4, 12)
	if 4 < start || 4 >= end {
		t.Fatalf("cursor column 4 not within [%d, %d)", start, end)
	}
	used := 0
	for i := start; i < end; i++ {
		if i > start {
			used++ // separator
		}
		used += widths[i]
	}
	if used > 12 {
		t.Errorf("visible range uses %d cells, want <= 12", used)
	}
}

func TestVisibleColumnRangeOnNoColumnsIsEmpty(t *testing.T) {
	start, end := visibleColumnRange(nil, 0, 20)
	if start != 0 || end != 0 {
		t.Errorf("got [%d, %d), want [0, 0)", start, end)
	}
}

func TestGridViewShowsHeaderAndNullMarker(t *testing.T) {
	g := newGrid([]string{"id", "note"}, [][]any{{int64(1), nil}, {int64(2), "hi"}})
	out := g.view(40, 10, true)
	if !contains(out, "id") || !contains(out, "note") {
		t.Errorf("view missing header:\n%s", out)
	}
	if !contains(out, nullLabel) {
		t.Errorf("view missing NULL marker:\n%s", out)
	}
}

func TestGridViewOnNoRowsSaysSo(t *testing.T) {
	g := newGrid([]string{"id"}, nil)
	out := g.view(20, 5, true)
	if !contains(out, "No rows.") {
		t.Errorf("view missing empty-rows message:\n%s", out)
	}
}

func TestGridViewOnNoColumnsSaysSo(t *testing.T) {
	g := newGrid(nil, nil)
	out := g.view(20, 5, true)
	if !contains(out, "No columns.") {
		t.Errorf("view missing no-columns message:\n%s", out)
	}
}
