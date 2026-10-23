package ui

import (
	"strings"
	"testing"
)

// TestHighlightSQLAddsANSIColorCodes checks that highlighting actually
// colors the text (spec: "DDL syntax-highlighted with chroma") rather than
// just returning it unchanged; the exact codes are chroma's business, not
// ours, so this only checks that an escape sequence shows up.
func TestHighlightSQLAddsANSIColorCodes(t *testing.T) {
	got := highlightSQL("CREATE TABLE widgets (id int);")
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("highlightSQL() = %q, want it to contain an ANSI escape sequence", got)
	}
}

// TestHighlightSQLKeepsIdentifiersReadable checks that a single-token
// identifier survives highlighting as a contiguous run of visible
// characters (no escape codes land inside it), since callers rely on that
// to search the highlighted text for a name.
func TestHighlightSQLKeepsIdentifiersReadable(t *testing.T) {
	got := highlightSQL(`CREATE TABLE "widgets" (id int);`)
	if !strings.Contains(got, "widgets") {
		t.Fatalf("highlightSQL() = %q, want it to still contain the identifier %q", got, "widgets")
	}
}

// TestHighlightSQLFallsBackToPlainTextWhenLexerIsMissing exercises the
// defensive path if chroma's "sql" lexer were ever unavailable, since
// unreadable DDL would be worse than unhighlighted DDL.
func TestHighlightSQLFallsBackToPlainTextWhenLexerIsMissing(t *testing.T) {
	got := highlightWithLexer("CREATE TABLE widgets (id int);", nil)
	if got != "CREATE TABLE widgets (id int);" {
		t.Errorf("highlightWithLexer() with a nil lexer = %q, want the plain text back", got)
	}
}
