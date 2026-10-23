package ui_test

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// e2eTreeWidth is the tree pane's fixed width, border included (treeWidth
// in model.go): the first e2eTreeWidth cells of every screen line belong
// to the tree.
const e2eTreeWidth = 34

// clearScreen is the sequence a full repaint (tea.ClearScreen) starts with.
var clearScreen = []byte("\x1b[2J")

// repaint forces a full repaint and returns its text, ANSI stripped, once
// the output has settled: the repaint has started arriving and nothing
// more has arrived for a few consecutive polls. A full repaint is what
// lets a test read content that did not change, which a cell-diffing
// render would otherwise not redraw.
func repaint(t *testing.T, tm *teatest.TestModel) string {
	t.Helper()
	_, _ = io.ReadAll(tm.Output()) // drop deltas from before this repaint
	tm.Send(tea.ClearScreen())
	var buf []byte
	quiet := 0
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		b, _ := io.ReadAll(tm.Output())
		if len(b) > 0 {
			buf, quiet = append(buf, b...), 0
			continue
		}
		if i := bytes.LastIndex(buf, clearScreen); i >= 0 {
			if quiet++; quiet >= 3 {
				return screenText(buf[i+len(clearScreen):])
			}
		}
	}
	t.Fatalf("no full repaint arrived; got:\n%s", ansi.Strip(string(buf)))
	return ""
}

// screenText replays a full repaint on a blank virtual screen and returns
// its rows as plain text lines. The renderer positions text with cursor
// movement (CSI H, d, G, C, A, B, carriage return, line feed) instead of
// writing every space, so stripping ANSI alone would lose the layout;
// every other escape sequence (styles, modes, OSC) is dropped.
func screenText(b []byte) string {
	var rows [][]rune
	row, col := 0, 0
	put := func(r rune) {
		for len(rows) <= row {
			rows = append(rows, nil)
		}
		for len(rows[row]) <= col {
			rows[row] = append(rows[row], ' ')
		}
		rows[row][col] = r
		col++
	}
	in := []rune(string(b))
	for i := 0; i < len(in); i++ {
		switch r := in[i]; {
		case r == '\n':
			row, col = row+1, 0
		case r == '\r':
			col = 0
		case r == 0x1b && i+1 < len(in) && in[i+1] == '[':
			j := i + 2
			for j < len(in) && (in[j] < 0x40 || in[j] > 0x7e) {
				j++
			}
			if j >= len(in) {
				i = j
				break
			}
			params := strings.Split(string(in[i+2:j]), ";")
			arg := func(k, def int) int {
				if k < len(params) {
					if n, err := strconv.Atoi(strings.TrimPrefix(params[k], "?")); err == nil && n > 0 {
						return n
					}
				}
				return def
			}
			switch in[j] {
			case 'H', 'f':
				row, col = arg(0, 1)-1, arg(1, 1)-1
			case 'd':
				row = arg(0, 1) - 1
			case 'G':
				col = arg(0, 1) - 1
			case 'C':
				col += arg(0, 1)
			case 'D':
				col = max(col-arg(0, 1), 0)
			case 'A':
				row = max(row-arg(0, 1), 0)
			case 'B':
				row += arg(0, 1)
			}
			i = j
		case r == 0x1b && i+1 < len(in) && in[i+1] == ']':
			j := i + 2
			for j < len(in) && in[j] != 0x07 && !(in[j] == 0x1b && j+1 < len(in) && in[j+1] == '\\') {
				j++
			}
			if j < len(in) && in[j] == 0x1b {
				j++
			}
			i = j
		case r == 0x1b:
			i++ // a two-byte escape
		case r < 0x20:
		default:
			put(r)
		}
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = string(r)
	}
	return strings.Join(lines, "\n")
}

// waitForScreen repaints until match accepts the screen and returns it.
func waitForScreen(t *testing.T, tm *teatest.TestModel, match func(screen string) bool) string {
	t.Helper()
	var last string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if last = repaint(t, tm); match(last) {
			return last
		}
	}
	t.Fatalf("no matching screen arrived; last:\n%s", last)
	return ""
}

// waitForText repaints until the screen shows want and returns it. Unlike
// waitForFrameContaining it reads the whole screen, so it also matches
// text the renderer only partly redrew (a delta that rewrote "TREE" as
// "TABS" sends just "ABS").
func waitForText(t *testing.T, tm *teatest.TestModel, want string) string {
	t.Helper()
	return waitForScreen(t, tm, func(s string) bool { return strings.Contains(s, want) })
}

// treeRows returns the tree pane's rows from a repainted screen, top
// first: the cells inside the pane's border, on the lines between its top
// and bottom edges (the screen's last line is the status bar).
func treeRows(screen string) []string {
	lines := strings.Split(screen, "\n")
	if len(lines) < 4 {
		return nil
	}
	var rows []string
	for _, line := range lines[1 : len(lines)-2] {
		r := []rune(strings.TrimLeft(line, "\r"))
		if len(r) > e2eTreeWidth-1 {
			r = r[:e2eTreeWidth-1]
		}
		if len(r) > 0 {
			r = r[1:]
		}
		rows = append(rows, string(r))
	}
	return rows
}

// treeFull reports whether the tree rows fill the pane to its bottom row,
// so more may be hidden below.
func treeFull(rows []string) bool {
	return len(rows) >= 1 && strings.TrimSpace(rows[len(rows)-1]) != ""
}

// treeSelected reports whether a tree row is the selected one: it starts
// with the selection bar.
func treeSelected(row string) bool { return strings.HasPrefix(row, "▌") }

// rowIsNode reports whether a tree row shows the node called name: after
// the selection bar column, the indentation, and the expand glyph, the
// label starts with name followed by the end or a space (so "hqdb" is not
// "hqdb2").
func rowIsNode(row, name string) bool {
	r := []rune(row)
	if len(r) < 2 {
		return false
	}
	label := strings.TrimLeft(string(r[1:]), " ")
	if l := []rune(label); len(l) > 1 && strings.ContainsRune("▾▸✕…+", l[0]) && l[1] == ' ' {
		label = string(l[2:])
	}
	return strings.HasPrefix(label, name) && (len(label) == len(name) || label[len(name)] == ' ')
}

// selectNode moves the tree selection onto the visible node called name
// by reading where the cursor and that node are on screen, so a test
// never depends on how many Profiles, Tables, or keys sort ahead of it.
// With several nodes of that name it picks the first one at or below the
// cursor. The tree must have focus. It ends with a full repaint so the
// next wait sees the whole screen again.
func selectNode(t *testing.T, tm *teatest.TestModel, name string) {
	t.Helper()
	var screen string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		screen = repaint(t, tm)
		rows := treeRows(screen)
		cur, target := -1, -1
		for i, r := range rows {
			if treeSelected(r) {
				cur = i
				break
			}
		}
		if cur < 0 {
			continue
		}
		for i, r := range rows {
			if rowIsNode(r, name) && (target < 0 || (target < cur && i >= cur)) {
				target = i
			}
		}
		switch {
		case target == cur:
			tm.Send(tea.ClearScreen())
			return
		case target > cur:
			sendKey(tm, tea.KeyDown, target-cur)
		case target >= 0:
			sendKey(tm, tea.KeyUp, cur-target)
		case treeFull(rows):
			// Not on screen but the tree may continue below: scroll a
			// screenful down and look again.
			sendKey(tm, tea.KeyDown, len(rows)-1)
		}
		// Otherwise the node has not loaded yet: look again.
	}
	t.Fatalf("could not select tree node %q; last screen:\n%s", name, screen)
}

// selectPath selects each name in turn, expanding every one but the last
// with Enter and waiting until the next name shows up under it.
func selectPath(t *testing.T, tm *teatest.TestModel, names ...string) {
	t.Helper()
	for i, name := range names {
		selectNode(t, tm, name)
		if i == len(names)-1 {
			return
		}
		enter(tm)
		next := names[i+1]
		waitForScreen(t, tm, func(s string) bool {
			for _, r := range treeRows(s) {
				if rowIsNode(r, next) {
					return true
				}
			}
			return false
		})
	}
}

func sendKey(tm *teatest.TestModel, code rune, n int) {
	for i := 0; i < n; i++ {
		tm.Send(tea.KeyPressMsg{Code: code})
	}
}

// uniqueName is prefix plus a per-run suffix, so a fixture object a test
// creates never collides with another run's or depends on sort position.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s%06d", prefix, time.Now().UnixNano()%1000000)
}
