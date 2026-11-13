package mask

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// randomASCII is a string of n bytes from alphabet.
func randomASCII(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

func TestFoldTextASCIIFastPath(t *testing.T) {
	slow := func(s string) string { return cases.Fold().String(norm.NFC.String(strings.TrimSpace(s))) }
	var all strings.Builder
	for c := 0; c < 0x80; c++ {
		all.WriteByte(byte(c))
	}
	inputs := []string{"", "  Ivan Petrov ", "KELVIN k", "ß", "Straße", "İstanbul", "K", all.String()}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		inputs = append(inputs, randomASCII(r, all.String(), r.Intn(40)))
	}
	for _, s := range inputs {
		if got, want := foldText(s), slow(s); got != want {
			t.Fatalf("foldText(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestNormalizeNameASCIIFastPath(t *testing.T) {
	inputs := []string{"", "id", "firstName", "HTTPServer", "Customer Email Address", "__a__b__", "A1b2C3", "ABCdef", "x-Y_z.W"}
	for _, r := range strings.Fields(nameRules + exactNameRules + specialRules) {
		if isASCII(r) {
			inputs = append(inputs, r)
		}
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		inputs = append(inputs, randomASCII(r, "aAbBzZ09_- .$", r.Intn(16)))
	}
	for _, s := range inputs {
		if got, want := normalizeASCIIName(s), normalizeUnicodeName(s); got != want {
			t.Fatalf("normalizeASCIIName(%q) = %q, want %q", s, got, want)
		}
	}
}

// linearMatches is ruleSet.matches as a scan over every sequence.
func linearMatches(rs ruleSet, name string) bool {
	if name == "" {
		return false
	}
	if rs.exact[name] {
		return true
	}
	tokens := strings.Split(name, "_")
	for _, seqs := range rs.seqs {
		for _, seq := range seqs {
			for i := range tokens {
				if hasRunAt(tokens, seq, i) {
					return true
				}
			}
		}
	}
	return false
}

func TestRuleIndexMatchesLinearScan(t *testing.T) {
	words := strings.Fields(nameRules + exactNameRules + specialRules + " id user client name of date code x")
	var names []string
	for _, w := range words {
		n := normalizeName(w)
		names = append(names, n, "user_"+n, n+"_2", "a_"+n+"_b", strings.ReplaceAll(n, "_", ""))
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		k := 1 + r.Intn(4)
		toks := make([]string, k)
		for j := range toks {
			toks[j] = normalizeName(words[r.Intn(len(words))])
		}
		names = append(names, strings.Join(toks, "_"))
	}
	for _, rs := range []ruleSet{builtinRules, specialSet} {
		for _, n := range names {
			if got, want := rs.matches(n), linearMatches(rs, n); got != want {
				t.Fatalf("matches(%q) = %v, linear scan %v", n, got, want)
			}
		}
	}
}

func TestClassifyCache(t *testing.T) {
	m := newMasker(t, map[string]any{"columns": []any{"client_*"}, "allow": []any{"product_name"}})
	paths := [][]string{
		{"email"}, {"product_name"}, {"client_code"}, {"diagnosis"}, {"status"},
		{"user", "email"}, {"user", "contact", "zipCode"}, {"a\x00b"}, {"a", "b"}, {"a\x00b", "c"}, {"a", "b\x00c"},
		{"first", "name"}, {"first_name"},
	}
	for round := 0; round < 3; round++ {
		for _, p := range paths {
			if got, want := m.classify(p), m.classifyNames(p); got != want {
				t.Fatalf("round %d: classify(%q) = %v, want %v", round, p, got, want)
			}
		}
	}
	// past the bound, results stay right and the cache stops growing
	for i := 0; i < maxCachedClasses+10; i++ {
		m.classify([]string{"col_" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + strings.Repeat("y", i/26)})
	}
	if len(m.names) > maxCachedClasses {
		t.Errorf("cache holds %d names, bound %d", len(m.names), maxCachedClasses)
	}
	if got := m.classify([]string{"phone_number"}); got != classNamed {
		t.Errorf("classify(phone_number) past the bound = %v", got)
	}
}

func TestMarkerReusesHMAC(t *testing.T) {
	m := newMasker(t, nil)
	fresh := func(text string) string {
		h := hmac.New(sha256.New, m.key)
		h.Write([]byte(text))
		return "masked-" + hex.EncodeToString(h.Sum(nil))[:12]
	}
	for _, s := range []string{"", "ivan@x.ru", strings.Repeat("x", 1000), "ivan@x.ru", "+79161234567"} {
		if got, want := m.marker(s), fresh(s); got != want {
			t.Errorf("marker(%q) = %s, want %s", s, got, want)
		}
	}
}

func TestIntegerFastPath(t *testing.T) {
	m := newMasker(t, nil)
	tests := []struct {
		v      any
		masked bool
	}{
		{int64(12345678), false},
		{int64(-99999999), false},
		{float64(89161234), false},
		{int64(7707083893), true},   // ru_inn
		{float64(7707083893), true}, // the same as a JSON number
		{int64(-7707083893), false},
		{uint64(4111111111111111), true}, // card
	}
	for _, tt := range tests {
		if _, changed := m.Value("n", tt.v); changed != tt.masked {
			t.Errorf("Value(%v) masked = %v, want %v", tt.v, changed, tt.masked)
		}
	}
}

// oldMarker is the marker screen as a regular expression.
var oldMarker = regexp.MustCompile(`\bmasked-(?:[0-9a-f]{12}|redacted)\b`)

func TestFindMarkerMatchesRegexp(t *testing.T) {
	inputs := []string{
		"", "masked-", "masked-redacted", "xmasked-redacted", "masked-redactedx", "masked-redacted-",
		"masked-3f9a1c2b4d5e", "masked-3f9a1c2b4d5", "masked-3f9a1c2b4d5ef", "masked-3f9a1c2b4d5e_",
		"_masked-3f9a1c2b4d5e", "émasked-3f9a1c2b4d5eé", "masked-masked-3f9a1c2b4d5e", "masked-redacte",
		"a masked-xyz and masked-0123456789ab.", "masked-redacted3f9a1c2b4d5e",
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := r.Intn(60); b.Len() < n; {
			switch r.Intn(6) {
			case 0:
				b.WriteString("masked-")
			case 1:
				b.WriteString("redacted")
			case 2:
				b.WriteString(randomASCII(r, "0123456789abcdef", r.Intn(14)))
			default:
				b.WriteString(randomASCII(r, "af09gz_ -.'\"\x80", 1))
			}
		}
		inputs = append(inputs, b.String())
	}
	for _, s := range inputs {
		if got, want := findMarker(s), oldMarker.FindString(s); got != want {
			t.Fatalf("findMarker(%q) = %q, regexp %q", s, got, want)
		}
	}
}
