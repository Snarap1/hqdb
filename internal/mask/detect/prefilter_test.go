package detect

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestDerivePrefilter(t *testing.T) {
	tests := []struct {
		expr     string
		alpha    string // "" means the filter is disabled
		highByte bool   // bytes from 0x80 up are in the alphabet
		minLen   int
		minDigit int
	}{
		{`\d{9}`, "0123456789", false, 9, 9},
		{`\d{3}[ -]\d{2}`, "-0123456789", false, 6, 5},
		{`(?i)DE ?\d{9}`, "0123456789DEde", false, 11, 9},
		// (?i)k folds to U+212A KELVIN SIGN, (?i)s to U+017F LONG S
		{`(?i)k\d`, "0123456789Kk", true, 2, 1},
		{`(?i)s\d`, "0123456789Ss", true, 2, 1},
		{`[\p{L}\p{N}_]+@`, "", true, 2, 0},
		{`\d{2}|\d{5}`, "0123456789", false, 2, 2},
		{`a*`, "", false, 0, 0},
		{`\bfoo`, "", false, 0, 0},
		{`^\d+$`, "", false, 0, 0},
		{`a.b`, "", false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			p := derivePrefilter(tt.expr)
			if tt.alpha == "" && !tt.highByte {
				if p.alpha != nil {
					t.Fatalf("filter = %s, want disabled", p.alpha)
				}
				return
			}
			if p.alpha == nil {
				t.Fatal("filter disabled")
			}
			for _, c := range tt.alpha {
				if !p.alpha[c] {
					t.Errorf("alphabet %s lacks %q", p.alpha, c)
				}
			}
			if p.alpha[0x80] != tt.highByte || p.alpha[0xff] != tt.highByte {
				t.Errorf("alphabet %s: high bytes %v, want %v", p.alpha, p.alpha[0x80], tt.highByte)
			}
			if p.minLen != tt.minLen {
				t.Errorf("minLen = %d, want %d", p.minLen, tt.minLen)
			}
			digits := 0
			for _, n := range p.needs {
				if n.set['0'] && !n.set['a'] {
					digits = n.min
				}
			}
			if digits != tt.minDigit {
				t.Errorf("min digits = %d, want %d", digits, tt.minDigit)
			}
		})
	}
}

// Every built-in expression has a filter: none uses anchors or any-char.
func TestEveryFinderIsFiltered(t *testing.T) {
	for _, d := range detectors {
		for i, f := range d.finders {
			if f.pf.alpha == nil {
				t.Errorf("%s finder %d has no prefilter", d.kind, i)
			}
		}
	}
}

// rawFind is Find without prefilters: every finder on the whole text,
// every candidate checked with rawMatch.
func rawFind(text string) []Span {
	var cands []candidate
	for prio, d := range detectors {
		for _, f := range d.finders {
			for _, loc := range f.find(text) {
				s, e := loc[0], loc[1]
				if e <= s || !bounded(text, s, e) || !d.rawMatch(text[s:e]) {
					continue
				}
				cands = append(cands, candidate{Span{s, e, d.kind}, prio})
			}
		}
	}
	return resolve(cands)
}

func rawMatchKind(s string) (Kind, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	for _, d := range detectors {
		if d.rawMatch(s) {
			return d.kind, true
		}
	}
	return "", false
}

func checkPrefilterEquivalence(t *testing.T, s string) {
	t.Helper()
	if got, want := Find(s), rawFind(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("Find(%q)\n got %v\nwant %v", s, got, want)
	}
	gk, gok := Match(s)
	wk, wok := rawMatchKind(s)
	if gk != wk || gok != wok {
		t.Fatalf("Match(%q) = %q, %v; unfiltered %q, %v", s, gk, gok, wk, wok)
	}
}

// prefilterCorpus is every valid sample, alone and in context, plus
// values with case folds, look-alikes, and separators at the edges.
func prefilterCorpus(t testing.TB) []string {
	var out []string
	for _, c := range validCases(t) {
		v := c.value
		out = append(out, v, "x "+v+" y", "("+v+")", v+".", "a"+v, v+"K", strings.ToLower(v), "ſ"+v)
	}
	out = append(out, notMatching...)
	out = append(out,
		"Иван@пример.рф", "7123456А001РВ", "de89370400440532013000", "KZ86125095",
		"::1", "fe80::1", "2001:db8::1:", "aa-bb-cc-dd-ee-ff", "0049 30 901820", "8 (916) 123-45-67",
		"call 030 1234567 12 now", "fr 40 303 265 045", "pl 123-456-32-18", "1 85 05 78 006 084 91",
		"Phones: 8 916 123-45-60 8 916 123-45-61 8 916 123-45-62 8 916 123-45-63 8 916 123-45-64",
		"\xff\xfe1234567890\xff", "a +49 30 901820",
	)
	return out
}

// randomText mixes the bytes the detectors care about with whole values,
// non-ASCII case folds, and invalid UTF-8.
func randomText(r *rand.Rand, n int) string {
	const pieces = "0123456789     --..::@@+()abcdefxyzABCDEFKLMPRSTXYZ_/,#"
	extra := []string{"K", "ſ", "А", "Р", "В", "é", "\xff", "ivan@x.ru", "+7 916 123-45-67", "4111 1111 1111 1111",
		"DE89 3704 0044 0532 0130 00", "7707083893", "192.168.1.10", "fe80::1", "aa:bb:cc:dd:ee:ff",
		"DE136695976", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.x", "AKIAIOSFODNN7EXAMPLE", "65929970489"}
	var b strings.Builder
	for b.Len() < n {
		if r.Intn(12) == 0 {
			b.WriteString(extra[r.Intn(len(extra))])
		} else {
			b.WriteByte(pieces[r.Intn(len(pieces))])
		}
	}
	return b.String()
}

func TestPrefilterEquivalence(t *testing.T) {
	for _, s := range prefilterCorpus(t) {
		checkPrefilterEquivalence(t, s)
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 3000; i++ {
		checkPrefilterEquivalence(t, randomText(r, 1+r.Intn(120)))
	}
}

func FuzzPrefilter(f *testing.F) {
	for _, s := range prefilterCorpus(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			return
		}
		checkPrefilterEquivalence(t, s)
	})
}
