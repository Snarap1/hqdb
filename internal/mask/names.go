package mask

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// letterFolds are Latin letters that do not decompose into a base letter
// plus a combining mark.
var letterFolds = map[rune]string{
	'ł': "l", 'Ł': "L", 'ø': "o", 'Ø': "O", 'đ': "d", 'Đ': "D",
	'æ': "ae", 'Æ': "AE", 'œ': "oe", 'Œ': "OE", 'ı': "i", 'ß': "ss", 'ẞ': "SS",
}

// normalizeName turns a column or field name into its rule-matching form:
// lower case, Latin diacritics removed, camelCase split, and every run of
// non-letter-non-digit characters turned into one "_", trimmed.
func normalizeName(s string) string {
	var base []rune
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if f, ok := letterFolds[r]; ok {
			base = append(base, []rune(f)...)
			continue
		}
		if r > unicode.MaxASCII && unicode.Is(unicode.Latin, r) {
			d := []rune(norm.NFD.String(string(r)))
			base = append(base, d[0])
			continue
		}
		base = append(base, r)
	}
	var b strings.Builder
	pendingSep := false
	for i, r := range base {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			pendingSep = true
			continue
		}
		if i > 0 {
			prev := base[i-1]
			if unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				pendingSep = true
			} else if unicode.IsUpper(r) && unicode.IsUpper(prev) &&
				i+1 < len(base) && unicode.IsLower(base[i+1]) {
				pendingSep = true
			}
		}
		if pendingSep && b.Len() > 0 {
			b.WriteByte('_')
		}
		pendingSep = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
