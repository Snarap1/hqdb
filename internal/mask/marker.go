package mask

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const redactedMarker = "masked-redacted"

var reMarker = regexp.MustCompile(`\bmasked-(?:[0-9a-f]{12}|redacted)\b`)

// marker returns "masked-" plus the first 12 hex characters of
// HMAC-SHA256(key, text).
func (m *Masker) marker(text string) string {
	h := hmac.New(sha256.New, m.key)
	h.Write([]byte(text))
	return "masked-" + hex.EncodeToString(h.Sum(nil))[:12]
}

// foldText is the hashed form of a value no detector recognized: trimmed,
// NFC, case-folded.
func foldText(s string) string {
	return cases.Fold().String(norm.NFC.String(strings.TrimSpace(s)))
}

// MarkerViolation returns the refusal reason when text contains a masked
// marker, or "". Markers are placeholders, so a statement that carries one
// is refused on a masked Profile.
func MarkerViolation(text string) string {
	mk := reMarker.FindString(text)
	if mk == "" {
		return ""
	}
	return fmt.Sprintf("statement contains a masked value (%s); masked values are placeholders, not data", mk)
}
