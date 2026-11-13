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
	if isASCII(s) {
		return normalizeASCIIName(s)
	}
	return normalizeUnicodeName(s)
}

// normalizeASCIIName is normalizeName for ASCII: no diacritics to remove,
// and the letter, digit, and case tests are the ASCII ones.
func normalizeASCIIName(s string) string {
	isUpper := func(c byte) bool { return c >= 'A' && c <= 'Z' }
	isLower := func(c byte) bool { return c >= 'a' && c <= 'z' }
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	b := make([]byte, 0, len(s)+4)
	pendingSep := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isUpper(c) && !isLower(c) && !isDigit(c) {
			pendingSep = true
			continue
		}
		if i > 0 {
			prev := s[i-1]
			if isUpper(c) && (isLower(prev) || isDigit(prev)) {
				pendingSep = true
			} else if isUpper(c) && isUpper(prev) && i+1 < len(s) && isLower(s[i+1]) {
				pendingSep = true
			}
		}
		if pendingSep && len(b) > 0 {
			b = append(b, '_')
		}
		pendingSep = false
		if isUpper(c) {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	return string(b)
}

func normalizeUnicodeName(s string) string {
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
