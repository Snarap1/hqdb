package ui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// editor is the Console's small multi-line text buffer (ADR-0002: our own
// component, so it can render chroma-highlighted SQL). It supports typing,
// newlines, deletion, and cursor movement; selection is out of scope.
type editor struct {
	buf []rune
	cur int // rune offset of the cursor, 0..len(buf)
}

// text returns the whole buffer.
func (e editor) text() string { return string(e.buf) }

// cursorByte returns the cursor as a byte offset into text().
func (e editor) cursorByte() int { return len(string(e.buf[:e.cur])) }

func (e editor) insert(s string) editor {
	r := []rune(s)
	buf := make([]rune, 0, len(e.buf)+len(r))
	buf = append(append(append(buf, e.buf[:e.cur]...), r...), e.buf[e.cur:]...)
	e.buf, e.cur = buf, e.cur+len(r)
	return e
}

// paste inserts pasted text at the cursor, line breaks normalized and tabs
// expanded the way the tab key types them.
func (e editor) paste(s string) editor {
	return e.insert(strings.ReplaceAll(normalizeNewlines(s), "\t", "  "))
}

// lineBounds returns the rune offsets of the start and end (exclusive of
// "\n") of the line holding offset i.
func (e editor) lineBounds(i int) (int, int) {
	start := i
	for start > 0 && e.buf[start-1] != '\n' {
		start--
	}
	end := i
	for end < len(e.buf) && e.buf[end] != '\n' {
		end++
	}
	return start, end
}

// moveLine moves the cursor one line up (dir < 0) or down, keeping its
// column where the target line is long enough.
func (e editor) moveLine(dir int) editor {
	start, end := e.lineBounds(e.cur)
	col := e.cur - start
	var ts, te int
	if dir < 0 {
		if start == 0 {
			return e
		}
		ts, te = e.lineBounds(start - 1)
	} else {
		if end == len(e.buf) {
			return e
		}
		ts, te = e.lineBounds(end + 1)
	}
	e.cur = min(ts+col, te)
	return e
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// wordStart is the offset of the start of the word before offset i (or of
// the one i is inside), skipping any separators, line breaks included.
func (e editor) wordStart(i int) int {
	for i > 0 && !isWordRune(e.buf[i-1]) {
		i--
	}
	for i > 0 && isWordRune(e.buf[i-1]) {
		i--
	}
	return i
}

// wordEnd is the offset of the end of the word after offset i (or of the
// one i is inside), skipping any separators, line breaks included.
func (e editor) wordEnd(i int) int {
	for i < len(e.buf) && !isWordRune(e.buf[i]) {
		i++
	}
	for i < len(e.buf) && isWordRune(e.buf[i]) {
		i++
	}
	return i
}

// deleteRange removes the runes in [from, to) and puts the cursor at from.
func (e editor) deleteRange(from, to int) editor {
	e.buf = append(e.buf[:from:from], e.buf[to:]...)
	e.cur = from
	return e
}

// openLineBelow starts a new line under the cursor's line and moves there,
// whatever the column.
func (e editor) openLineBelow() editor {
	_, e.cur = e.lineBounds(e.cur)
	return e.insert("\n")
}

// swapLine swaps the cursor's line with the one above (dir < 0) or below,
// the cursor going along with its line at the same column.
func (e editor) swapLine(dir int) editor {
	start, end := e.lineBounds(e.cur)
	col := e.cur - start
	var from, to int // the two lines' span, the upper one first
	var upper, lower []rune
	if dir < 0 {
		if start == 0 {
			return e
		}
		ps, pe := e.lineBounds(start - 1)
		from, to = ps, end
		upper, lower = e.buf[start:end], e.buf[ps:pe]
	} else {
		if end == len(e.buf) {
			return e
		}
		ns, ne := e.lineBounds(end + 1)
		from, to = start, ne
		upper, lower = e.buf[ns:ne], e.buf[start:end]
	}
	swapped := append(append(append([]rune{}, upper...), '\n'), lower...)
	buf := append(append(append([]rune{}, e.buf[:from]...), swapped...), e.buf[to:]...)
	e.buf = buf
	if dir < 0 {
		e.cur = from + col
	} else {
		e.cur = from + len(upper) + 1 + col
	}
	return e
}

// duplicateLine copies the cursor's line below itself and moves the cursor
// to the copy at the same column.
func (e editor) duplicateLine() editor {
	start, end := e.lineBounds(e.cur)
	col := e.cur - start
	line := string(e.buf[start:end])
	e.cur = end
	e = e.insert("\n" + line)
	e.cur = end + 1 + col
	return e
}

// update applies one key press to the buffer.
func (e editor) update(k tea.KeyPressMsg) editor {
	switch k.String() {
	case "enter":
		return e.insert("\n")
	case "shift+enter":
		return e.openLineBelow()
	case "ctrl+left", "alt+left", "alt+b":
		e.cur = e.wordStart(e.cur)
	case "ctrl+right", "alt+right", "alt+f":
		e.cur = e.wordEnd(e.cur)
	case "ctrl+backspace":
		return e.deleteRange(e.wordStart(e.cur), e.cur)
	case "ctrl+delete":
		return e.deleteRange(e.cur, e.wordEnd(e.cur))
	case "alt+up":
		return e.swapLine(-1)
	case "alt+down":
		return e.swapLine(1)
	case "ctrl+d":
		return e.duplicateLine()
	case "ctrl+home":
		e.cur = 0
	case "ctrl+end":
		e.cur = len(e.buf)
	case "ctrl+u":
		start, _ := e.lineBounds(e.cur)
		return e.deleteRange(start, e.cur)
	case "ctrl+k":
		_, end := e.lineBounds(e.cur)
		return e.deleteRange(e.cur, end)
	case "tab":
		return e.insert("  ")
	case "backspace":
		if e.cur > 0 {
			e.buf = append(e.buf[:e.cur-1:e.cur-1], e.buf[e.cur:]...)
			e.cur--
		}
	case "delete":
		if e.cur < len(e.buf) {
			e.buf = append(e.buf[:e.cur:e.cur], e.buf[e.cur+1:]...)
		}
	case "left":
		e.cur = max(0, e.cur-1)
	case "right":
		e.cur = min(len(e.buf), e.cur+1)
	case "up":
		return e.moveLine(-1)
	case "down":
		return e.moveLine(1)
	case "home":
		e.cur, _ = e.lineBounds(e.cur)
	case "end":
		_, e.cur = e.lineBounds(e.cur)
	default:
		if k.Text != "" {
			return e.insert(k.Text)
		}
	}
	return e
}

var editorCursorStyle = lipgloss.NewStyle().Background(colorAccent).Foreground(colorOnBadge)

// highlightLine highlights one line of SQL, dropping the trailing newline
// chroma may add.
func highlightLine(s string) string {
	if s == "" {
		return ""
	}
	return strings.TrimRight(highlightSQL(s), "\n")
}

// view renders the buffer syntax-highlighted with line numbers, height
// lines tall, scrolled to keep the cursor's line visible, with the cursor
// drawn when focused.
func (e editor) view(width, height int, focused bool) string {
	lines := strings.Split(string(e.buf), "\n")
	curLine := strings.Count(string(e.buf[:e.cur]), "\n")
	lineStart, _ := e.lineBounds(e.cur)
	col := e.cur - lineStart
	top := scrollTopFor(curLine, len(lines), height)
	gutterW := max(len(fmt.Sprint(len(lines))), 2)
	gutter := lipgloss.NewStyle().Foreground(colorGutter).Width(gutterW).Align(lipgloss.Right)
	clip := lipgloss.NewStyle().Inline(true).MaxWidth(max(width-gutterW-1, 1))
	out := make([]string, 0, height)
	for i := top; i < len(lines) && len(out) < height; i++ {
		line := lines[i]
		num := gutter
		if i == curLine {
			num = num.Foreground(colorSubtle)
		}
		if i == curLine && focused {
			r := []rune(line)
			under := " "
			after := ""
			if col < len(r) {
				under, after = string(r[col]), string(r[col+1:])
			}
			line = highlightLine(string(r[:col])) + editorCursorStyle.Render(under) + highlightLine(after)
		} else {
			line = highlightLine(line)
		}
		out = append(out, num.Render(fmt.Sprint(i+1))+" "+clip.Render(line))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}
