package mask

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const redactedMarker = "masked-redacted"

// marker returns "masked-" plus the first 12 hex characters of
// HMAC-SHA256(key, text). The HMAC is made once per Masker and reset for
// every marker.
func (m *Masker) marker(text string) string {
	if m.mac == nil {
		m.mac = hmac.New(sha256.New, m.key)
	}
	m.mac.Reset()
	io.WriteString(m.mac, text)
	m.sum = m.mac.Sum(m.sum[:0])
	var out [len("masked-") + 12]byte
	copy(out[:], "masked-")
	hex.Encode(out[len("masked-"):], m.sum[:6])
	return string(out[:])
}

// foldText is the hashed form of a value no detector recognized: trimmed,
// NFC, case-folded. ASCII text is already NFC, and its case fold is its
// lower case.
func foldText(s string) string {
	s = strings.TrimSpace(s)
	if isASCII(s) {
		return strings.ToLower(s)
	}
	return cases.Fold().String(norm.NFC.String(s))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// isWordByte is the ASCII word class of regexp \b.
func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isLowerHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' }

// findMarker returns the first marker in text, or "": "masked-" plus 12
// lower-case hex characters or "redacted", standing alone between ASCII
// word boundaries (the \b of regexp).
func findMarker(text string) string {
	const lit = "masked-"
	for off := 0; ; {
		i := strings.Index(text[off:], lit)
		if i < 0 {
			return ""
		}
		i += off
		off = i + 1
		if i > 0 && isWordByte(text[i-1]) {
			continue
		}
		end := markerBody(text, i+len(lit))
		if end < 0 || end < len(text) && isWordByte(text[end]) {
			continue
		}
		return text[i:end]
	}
}

// markerBody returns the end of the marker body that starts at text[i:],
// or -1. The two bodies start with different characters (r is not hex),
// so at most one can fit.
func markerBody(text string, i int) int {
	rest := text[i:]
	if strings.HasPrefix(rest, "redacted") {
		return i + len("redacted")
	}
	if len(rest) < 12 {
		return -1
	}
	for k := 0; k < 12; k++ {
		if !isLowerHex(rest[k]) {
			return -1
		}
	}
	return i + 12
}

// MarkerViolation returns the refusal reason when text contains a masked
// marker, or "". Markers are placeholders, so a statement that carries one
// is refused on a masked Profile.
func MarkerViolation(text string) string {
	mk := findMarker(text)
	if mk == "" {
		return ""
	}
	return fmt.Sprintf("statement contains a masked value (%s); masked values are placeholders, not data", mk)
}
