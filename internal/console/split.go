// Package console is the terminal UI's SQL Console core: finding the
// statement under the cursor in a multi-statement buffer, and running one
// statement on a pinned session. It has no Bubble Tea dependency and, like
// the other core packages, never screens what it runs (ADR-0003).
package console

import "strings"

// StatementAt returns the statement of script that the byte offset cursor
// sits in, trimmed of surrounding whitespace and its terminating ";".
// Statements are separated by ";" outside single-quoted strings,
// double-quoted, backtick, and bracket identifiers, line and block
// comments, and postgres dollar-quoted strings. A cursor right after a ";"
// belongs to the statement that ";" ends; a cursor in trailing whitespace
// after the last statement picks that last statement. It returns "" when
// the buffer has no statement at all.
func StatementAt(script string, cursor int) string {
	cursor = max(0, min(cursor, len(script)))
	segs := segments(script)
	pick := 0
	for i, s := range segs {
		if cursor > s[0] || i == 0 {
			pick = i
		}
	}
	for i := pick; i >= 0; i-- {
		if stmt := trimStatement(script[segs[i][0]:segs[i][1]]); stmt != "" {
			return stmt
		}
	}
	return ""
}

func trimStatement(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ";")
	return strings.TrimSpace(s)
}

// segments splits script into half-open [start, end) byte ranges, each
// ending just past its top-level ";" (the last one ends at len(script)).
func segments(script string) [][2]int {
	var out [][2]int
	start := 0
	code := codeOnly(script)
	for i := 0; i < len(code); i++ {
		if code[i] == ';' {
			out = append(out, [2]int{start, i + 1})
			start = i + 1
		}
	}
	return append(out, [2]int{start, len(script)})
}

// codeOnly returns script with every string, quoted identifier, comment,
// and dollar-quoted run blanked to spaces, so byte offsets are preserved
// and only top-level SQL text remains.
func codeOnly(script string) string {
	b := []byte(script)
	blank := func(from, to int) {
		for k := from; k < to; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	for i := 0; i < len(script); {
		c := script[i]
		next := i + 1
		switch {
		case c == '\'' || c == '"' || c == '`':
			next = skipQuoted(script, i, c)
		case c == '[':
			next = skipUntil(script, i+1, "]")
		case c == '-' && strings.HasPrefix(script[i:], "--"):
			next = skipUntil(script, i+2, "\n")
		case c == '/' && strings.HasPrefix(script[i:], "/*"):
			next = skipUntil(script, i+2, "*/")
		case c == '$':
			if tag, ok := dollarTag(script, i); ok {
				next = skipUntil(script, i+len(tag), tag)
			}
		}
		if next > i+1 {
			blank(i, next)
		}
		i = next
	}
	return string(b)
}

// skipQuoted returns the offset just past the quoted run opening at i with
// quote q, where a doubled quote is an escaped one; an unterminated run
// extends to the end.
func skipQuoted(s string, i int, q byte) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == q {
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(s)
}

// skipUntil returns the offset just past the first end at or after i, or
// len(s) when there is none.
func skipUntil(s string, i int, end string) int {
	if k := strings.Index(s[i:], end); k >= 0 {
		return i + k + len(end)
	}
	return len(s)
}

// dollarTag reports the dollar-quote opener ("$$" or "$tag$") at i. A "$"
// inside an identifier (postgres allows it after the first character) or
// followed by a digit (a positional parameter) opens nothing.
func dollarTag(s string, i int) (string, bool) {
	if i > 0 && isIdent(s[i-1]) {
		return "", false
	}
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[i : j+1], true
		}
		if !isIdent(c) || (j == i+1 && c >= '0' && c <= '9') {
			return "", false
		}
	}
	return "", false
}

func isIdent(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c >= 0x80
}
