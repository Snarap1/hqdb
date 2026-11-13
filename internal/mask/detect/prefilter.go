package detect

import (
	"regexp/syntax"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A prefilter is a cheap byte-level test derived from a regular
// expression: every string the expression matches consists of bytes of
// alpha only, is at least minLen bytes long, and carries at least the
// counts in needs. A string that fails the test cannot match, so the
// expression is not run on it.
//
// It is exact for Find as well: the expressions have no anchors, word
// boundaries, or any-character classes (derivePrefilter disables the
// filter otherwise), so a match never extends over a byte outside alpha,
// and running the expression on each maximal run of alpha bytes finds
// exactly the matches it finds on the whole text.
//
// Bytes from 0x80 up are in alpha as soon as the expression can match any
// non-ASCII rune, so a run never splits a UTF-8 sequence. Case folding is
// taken from the parsed expression: (?i)k also matches U+212A KELVIN SIGN
// and (?i)s U+017F LATIN SMALL LETTER LONG S, which puts the high bytes in
// alpha for every (?i) shape with a k or an s.
type prefilter struct {
	alpha  *byteSet // nil: no filter, the expression runs on the whole text
	minLen int
	needs  []need
}

type byteSet [256]bool

// need is a lower bound on the number of bytes of set in a match.
type need struct {
	set *byteSet
	min int
}

// admits reports whether s may match: all its bytes are in alpha and it
// carries the needed counts.
func (p *prefilter) admits(s string) bool {
	if p.alpha == nil {
		return true
	}
	for i := 0; i < len(s); i++ {
		if !p.alpha[s[i]] {
			return false
		}
	}
	return p.runAdmits(s)
}

// runAdmits is admits for a run already known to be inside alpha.
func (p *prefilter) runAdmits(run string) bool {
	if len(run) < p.minLen {
		return false
	}
	for _, n := range p.needs {
		c := 0
		for i := 0; i < len(run) && c < n.min; i++ {
			if n.set[run[i]] {
				c++
			}
		}
		if c < n.min {
			return false
		}
	}
	return true
}

// derivePrefilter parses expr as regexp.MustCompile does and derives its
// prefilter. Anything the derivation does not understand disables the
// filter instead of guessing.
func derivePrefilter(expr string) prefilter {
	re, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		return prefilter{}
	}
	re = re.Simplify()
	alpha := &byteSet{}
	if !collectAlphabet(re, alpha) {
		return prefilter{}
	}
	all := &byteSet{}
	for i := range all {
		all[i] = true
	}
	p := prefilter{alpha: alpha, minLen: minCount(re, all)}
	if p.minLen == 0 {
		return prefilter{} // an empty match could sit outside every run
	}
	for _, set := range candidateSets(alpha) {
		if n := minCount(re, set); n > 0 {
			p.needs = append(p.needs, need{set, n})
		}
	}
	return p
}

// collectAlphabet adds every byte re can match to alpha and reports
// whether re is made only of literals, classes, and repetitions.
func collectAlphabet(re *syntax.Regexp, alpha *byteSet) bool {
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			for _, f := range runeFolds(r, re.Flags) {
				addRune(alpha, f)
			}
		}
	case syntax.OpCharClass:
		for i := 0; i+1 < len(re.Rune); i += 2 {
			lo, hi := re.Rune[i], re.Rune[i+1]
			for r := lo; r <= hi && r < utf8.RuneSelf; r++ {
				alpha[r] = true
			}
			if hi >= utf8.RuneSelf {
				addRune(alpha, utf8.RuneSelf)
			}
		}
	case syntax.OpEmptyMatch, syntax.OpNoMatch:
	case syntax.OpCapture, syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat,
		syntax.OpConcat, syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !collectAlphabet(sub, alpha) {
				return false
			}
		}
	default: // anchors, word boundaries, any character
		return false
	}
	return true
}

// runeFolds is r and, under (?i), every rune it folds to.
func runeFolds(r rune, flags syntax.Flags) []rune {
	out := []rune{r}
	if flags&syntax.FoldCase != 0 {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			out = append(out, f)
		}
	}
	return out
}

// addRune adds the bytes of r to alpha; a non-ASCII rune adds every byte
// from 0x80 up, which covers all its UTF-8 encodings and U+FFFD for
// invalid input.
func addRune(alpha *byteSet, r rune) {
	if r < utf8.RuneSelf {
		alpha[r] = true
		return
	}
	for b := utf8.RuneSelf; b < 256; b++ {
		alpha[b] = true
	}
}

// runeMin is a lower bound on the number of bytes of set in the UTF-8
// form of r: 1 when r is an ASCII byte of set or a non-ASCII rune and set
// holds every byte from 0x80 up, else 0.
func runeMin(r rune, set *byteSet) int {
	if r < utf8.RuneSelf {
		if set[r] {
			return 1
		}
		return 0
	}
	for b := utf8.RuneSelf; b < 256; b++ {
		if !set[b] {
			return 0
		}
	}
	return 1
}

// minCount is a lower bound on the number of bytes of set in any string
// re matches.
func minCount(re *syntax.Regexp, set *byteSet) int {
	switch re.Op {
	case syntax.OpLiteral:
		n := 0
		for _, r := range re.Rune {
			m := 1
			for _, f := range runeFolds(r, re.Flags) {
				m = min(m, runeMin(f, set))
			}
			n += m
		}
		return n
	case syntax.OpCharClass:
		m := 1
		for i := 0; i+1 < len(re.Rune) && m > 0; i += 2 {
			lo, hi := re.Rune[i], re.Rune[i+1]
			for r := lo; r <= hi && r < utf8.RuneSelf; r++ {
				m = min(m, runeMin(r, set))
			}
			if hi >= utf8.RuneSelf {
				m = min(m, runeMin(utf8.RuneSelf, set))
			}
		}
		if len(re.Rune) == 0 {
			return 0
		}
		return m
	case syntax.OpCapture, syntax.OpPlus:
		return minCount(re.Sub[0], set)
	case syntax.OpRepeat:
		return re.Min * minCount(re.Sub[0], set)
	case syntax.OpConcat:
		n := 0
		for _, sub := range re.Sub {
			n += minCount(sub, set)
		}
		return n
	case syntax.OpAlternate:
		n := -1
		for _, sub := range re.Sub {
			if c := minCount(sub, set); n < 0 || c < n {
				n = c
			}
		}
		return max(n, 0)
	}
	return 0 // star, quest, empty match
}

// candidateSets are the byte sets whose counts a prefilter may require:
// ASCII digits, letters (with every non-ASCII byte), and each ASCII
// punctuation byte of the alphabet alone.
func candidateSets(alpha *byteSet) []*byteSet {
	digits, letters := &byteSet{}, &byteSet{}
	for c := '0'; c <= '9'; c++ {
		digits[c] = true
	}
	for c := 'a'; c <= 'z'; c++ {
		letters[c] = true
		letters[c-'a'+'A'] = true
	}
	for b := utf8.RuneSelf; b < 256; b++ {
		letters[b] = true
	}
	sets := []*byteSet{digits, letters}
	for c := 0; c < utf8.RuneSelf; c++ {
		if alpha[c] && !digits[c] && !letters[c] {
			one := &byteSet{}
			one[c] = true
			sets = append(sets, one)
		}
	}
	return sets
}

// finder proposes candidate byte ranges for one detector. find must only
// depend on the matches of the expression pf was derived from, so that
// it finds the same ranges on a run as on the whole text.
type finder struct {
	pf   prefilter
	find func(text string) [][2]int
}

// String lists the bytes of the set, printable ASCII as itself and the
// rest as \xNN; the tests use it to show derived alphabets.
func (s *byteSet) String() string {
	var b strings.Builder
	for c := 0; c < 256; c++ {
		if s[c] {
			if c < utf8.RuneSelf && c > ' ' {
				b.WriteByte(byte(c))
			} else {
				b.WriteString("\\x")
				b.WriteByte("0123456789abcdef"[c>>4])
				b.WriteByte("0123456789abcdef"[c&15])
			}
		}
	}
	return b.String()
}
