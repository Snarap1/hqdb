package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEditorNavigationAndWordEdits(t *testing.T) {
	ctrl := func(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl} }
	alt := func(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: tea.ModAlt} }
	tests := []struct {
		name    string
		text    string
		cur     int
		key     tea.KeyPressMsg
		wantTxt string
		wantCur int
	}{
		{"word right to end of word", "select a_b from", 0, ctrl(tea.KeyRight), "select a_b from", 6},
		{"word right skips separators", "select a_b from", 6, ctrl(tea.KeyRight), "select a_b from", 10},
		{"word right crosses a line break", "ab\n  cd", 2, ctrl(tea.KeyRight), "ab\n  cd", 7},
		{"word right at end stays", "ab", 2, ctrl(tea.KeyRight), "ab", 2},
		{"word left to start of word", "select a_b from", 10, ctrl(tea.KeyLeft), "select a_b from", 7},
		{"word left from inside a word", "select", 4, ctrl(tea.KeyLeft), "select", 0},
		{"word left crosses a line break", "ab\n  cd", 5, ctrl(tea.KeyLeft), "ab\n  cd", 0},
		{"word left at start stays", "ab", 0, ctrl(tea.KeyLeft), "ab", 0},
		{"ctrl+backspace deletes word", "select a_b", 10, ctrl(tea.KeyBackspace), "select ", 7},
		{"ctrl+delete deletes word", "select a_b", 0, ctrl(tea.KeyDelete), " a_b", 0},
		{"ctrl+home", "a\nb", 3, ctrl(tea.KeyHome), "a\nb", 0},
		{"ctrl+end", "a\nb", 0, ctrl(tea.KeyEnd), "a\nb", 3},
		{"ctrl+u deletes to line start", "a\nbcd", 4, ctrl('u'), "a\nd", 2},
		{"ctrl+k deletes to line end", "abc\nd", 1, ctrl('k'), "a\nd", 1},
		{"shift+enter opens a line below from mid-line", "abc\nd", 1, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "abc\n\nd", 4},
		{"shift+enter on the last line", "abc", 1, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "abc\n", 4},
		{"alt+up moves the line up", "ab\ncd\nef", 4, alt(tea.KeyUp), "cd\nab\nef", 1},
		{"alt+up on the first line stays", "ab\ncd", 1, alt(tea.KeyUp), "ab\ncd", 1},
		{"alt+down moves the line down", "ab\ncd\nef", 1, alt(tea.KeyDown), "cd\nab\nef", 4},
		{"alt+down on the last line stays", "ab\ncd", 4, alt(tea.KeyDown), "ab\ncd", 4},
		{"alt+down into an empty last line", "ab\n", 1, alt(tea.KeyDown), "\nab", 2},
		{"ctrl+d duplicates the line", "ab\ncd", 1, ctrl('d'), "ab\nab\ncd", 4},
		{"ctrl+d on the last line", "ab", 2, ctrl('d'), "ab\nab", 5},
		{"enter splits the line", "abc", 1, tea.KeyPressMsg{Code: tea.KeyEnter}, "a\nbc", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := editor{}.insert(tc.text)
			e.cur = tc.cur
			e = e.update(tc.key)
			if e.text() != tc.wantTxt || e.cur != tc.wantCur {
				t.Errorf("got %q cur %d, want %q cur %d", e.text(), e.cur, tc.wantTxt, tc.wantCur)
			}
		})
	}
}
