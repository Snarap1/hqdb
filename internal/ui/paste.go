package ui

import "strings"

// paster is implemented by a tab with a text field, so a terminal paste
// (bracketed paste arrives as one tea.PasteMsg, not as key presses) lands
// in the field that has focus; a tab ignores it when none does.
type paster interface {
	Paste(text string)
}

// normalizeNewlines turns CRLF and lone CR line breaks, which terminals
// send for pasted newlines, into "\n".
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// singleLine flattens pasted text for a one-line field: every line break
// becomes a space, and surrounding line breaks are dropped.
func singleLine(s string) string {
	return strings.ReplaceAll(strings.Trim(normalizeNewlines(s), "\n"), "\n", " ")
}

// digitsOnly keeps only the ASCII digits of s, for the page-size field.
func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
