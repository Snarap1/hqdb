package console

import (
	"strings"
	"testing"
)

// The cursor is marked with "|" in each script; StatementAt receives the
// script without it and the cursor's byte offset.
func TestStatementAt(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{"single statement", "SELECT |1", "SELECT 1"},
		{"cursor at end without semicolon", "SELECT 1|", "SELECT 1"},
		{"first of two", "SELECT |1; SELECT 2;", "SELECT 1"},
		{"second of two", "SELECT 1; SELECT |2;", "SELECT 2"},
		{"cursor right after semicolon belongs to the statement it ends", "SELECT 1;| SELECT 2;", "SELECT 1"},
		{"cursor right before semicolon", "SELECT 1|; SELECT 2;", "SELECT 1"},
		{"cursor at start of buffer", "|SELECT 1; SELECT 2", "SELECT 1"},
		{"cursor at start of second statement", "SELECT 1;\n|SELECT 2", "SELECT 2"},
		{"cursor at end of buffer after trailing semicolon", "SELECT 1; SELECT 2;|", "SELECT 2"},
		{"cursor on blank line after last statement picks previous", "SELECT 1;\n\n|", "SELECT 1"},
		{"semicolon in single-quoted string", "SELECT 'a;b' |AS x; SELECT 2", "SELECT 'a;b' AS x"},
		{"escaped quote in string", "SELECT 'it''s;' |x; SELECT 2", "SELECT 'it''s;' x"},
		{"semicolon in double-quoted identifier", `SELECT "a;b" |FROM t; SELECT 2`, `SELECT "a;b" FROM t`},
		{"semicolon in backtick identifier", "SELECT `a;b` |FROM t; SELECT 2", "SELECT `a;b` FROM t"},
		{"semicolon in bracket identifier", "SELECT [a;b] |FROM t; SELECT 2", "SELECT [a;b] FROM t"},
		{"semicolon in line comment", "SELECT 1 -- x; y\n|+ 1; SELECT 2", "SELECT 1 -- x; y\n+ 1"},
		{"semicolon in block comment", "SELECT /* ; */ |1; SELECT 2", "SELECT /* ; */ 1"},
		{"dollar quoting", "DO $$ BEGIN; END $$|; SELECT 2", "DO $$ BEGIN; END $$"},
		{"tagged dollar quoting", "SELECT $fn$ a; $$ b $fn$ |x; SELECT 2", "SELECT $fn$ a; $$ b $fn$ x"},
		{"positional parameter is not a dollar quote", "SELECT $1; SELECT |2", "SELECT 2"},
		{"empty buffer", "|", ""},
		{"only whitespace", "  |  ", ""},
		{"unterminated string swallows the rest", "SELECT 'a; |b", "SELECT 'a; b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur := strings.Index(c.script, "|")
			if cur < 0 {
				t.Fatalf("no cursor marker")
			}
			script := c.script[:cur] + c.script[cur+1:]
			if got := StatementAt(script, cur); got != c.want {
				t.Errorf("StatementAt(%q, %d) = %q, want %q", script, cur, got, c.want)
			}
		})
	}
}
