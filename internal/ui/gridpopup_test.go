package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+n":
		return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func TestEnterOpensPopupWithFullLongValueAndEscClosesIt(t *testing.T) {
	long := strings.Repeat("x", 60) + "END"
	g := newGrid([]string{"v"}, [][]any{{long}})
	g, _ = g.update(press("enter"), defaultKeyMap())
	view := g.view(40, 20, true)
	// The value wraps inside the box; dropping the box and gutter
	// characters must leave it whole.
	flat := strings.NewReplacer("\n", "", "│", "", " ", "").Replace(ansi.Strip(view))
	if !strings.Contains(flat, long) {
		t.Fatalf("popup must show the full value, got:\n%s", view)
	}
	g, _ = g.update(press("esc"), defaultKeyMap())
	if g.popupOpen() {
		t.Fatal("esc must close the popup")
	}
}

func TestPopupPrettyPrintsJSONValues(t *testing.T) {
	cases := []any{`{"a":1,"b":[2]}`, map[string]any{"a": 1.0, "b": []any{2.0}}}
	for _, v := range cases {
		got := popupText(v)
		want := "{\n  \"a\": 1,\n  \"b\": [\n    2\n  ]\n}"
		if got != want {
			t.Errorf("popupText(%#v) = %q, want %q", v, got, want)
		}
	}
	if got := popupText("plain"); got != "plain" {
		t.Errorf("non-JSON string must pass through, got %q", got)
	}
	if got := popupText(nil); got != nullLabel {
		t.Errorf("NULL popup = %q", got)
	}
}

func TestCellAndRowCopyText(t *testing.T) {
	g := newGrid([]string{"id", "name", "note"}, [][]any{{int64(1), "bolt", nil}})
	g = g.moveCursor(0, 1)
	if got, _ := g.cellCopyText(); got != "bolt" {
		t.Errorf("cell copy = %q", got)
	}
	if got, _ := g.rowCopyText(); got != `{"id":1,"name":"bolt","note":null}` {
		t.Errorf("row copy = %q", got)
	}
	if _, ok := newGrid([]string{"a"}, nil).cellCopyText(); ok {
		t.Error("an empty grid has nothing to copy")
	}
}

func TestSearchJumpsCursorBetweenMatchesAndHighlights(t *testing.T) {
	g := newGrid([]string{"a", "b"}, [][]any{{"apple", "x"}, {"y", "Pineapple"}, {"z", "w"}})
	k := defaultKeyMap()
	for _, s := range []string{"/", "a", "p", "p"} {
		g, _ = g.update(press(s), k)
	}
	if !g.searchEditing() {
		t.Fatal("/ must open the search field")
	}
	g, _ = g.update(press("enter"), k)
	if g.searchEditing() || g.cursorRow != 0 || g.cursorCol != 0 {
		t.Fatalf("enter: editing=%v cursor=(%d,%d), want first match (0,0)", g.searchEditing(), g.cursorRow, g.cursorCol)
	}
	g, _ = g.update(press("ctrl+n"), k)
	if g.cursorRow != 1 || g.cursorCol != 1 {
		t.Fatalf("next match: cursor=(%d,%d), want (1,1) (case-insensitive)", g.cursorRow, g.cursorCol)
	}
	g, _ = g.update(press("ctrl+n"), k)
	if g.cursorRow != 0 || g.cursorCol != 0 {
		t.Fatalf("next match must wrap, got (%d,%d)", g.cursorRow, g.cursorCol)
	}
	g, _ = g.update(press("ctrl+p"), k)
	if g.cursorRow != 1 || g.cursorCol != 1 {
		t.Fatalf("prev match must wrap, got (%d,%d)", g.cursorRow, g.cursorCol)
	}
	if !g.isMatch(1, 1) || g.isMatch(2, 0) {
		t.Fatal("isMatch must reflect the query")
	}
	if got := g.searchStatus(); got != "search: app (2/2)" {
		t.Errorf("searchStatus = %q", got)
	}
}

func TestCellAtMapsClickToCell(t *testing.T) {
	g := newGrid([]string{"id", "name"}, [][]any{{int64(1), "a"}, {int64(2), "b"}})
	// widths 4,4 separated by one space; line 0 is the header.
	r, c, ok := g.cellAt(6, 2, 40, 10)
	if !ok || r != 1 || c != 1 {
		t.Errorf("cellAt = (%d,%d,%v), want (1,1,true)", r, c, ok)
	}
	if _, _, ok := g.cellAt(0, 0, 40, 10); ok {
		t.Error("header click must not hit a cell")
	}
}
