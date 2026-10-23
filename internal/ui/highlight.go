package ui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlightSQL renders sql with syntax highlighting as ANSI escape
// sequences for the terminal (spec: "DDL syntax-highlighted ... so that I
// can reuse it"; ADR-0002 leaves library choices like this one to us).
func highlightSQL(sql string) string {
	return highlightWithLexer(sql, lexers.Get("sql"))
}

// highlightJSON renders pretty-printed JSON highlighted the same way, for
// the cell popup.
func highlightJSON(text string) string {
	return highlightWithLexer(text, lexers.Get("json"))
}

// highlightWithLexer is highlightSQL with an explicit lexer, so tests can
// pin the fallback paths without depending on chroma's registry having
// (or losing) an entry. It falls back to the plain text when there is no
// lexer or chroma fails. The github-dark style matches the UI palette
// (theme.go); colors are emitted in true color and Bubble Tea downsamples
// them for the terminal.
func highlightWithLexer(text string, lexer chroma.Lexer) string {
	if lexer == nil {
		return text
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		return text
	}
	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Fallback
	}
	var b strings.Builder
	if err := formatters.TTY16m.Format(&b, style, iterator); err != nil {
		return text
	}
	return b.String()
}
