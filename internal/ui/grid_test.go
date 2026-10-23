package ui

import "testing"

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
