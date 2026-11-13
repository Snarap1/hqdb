// Package detect recognizes personal data by value: emails, phones,
// network addresses, payment and bank numbers, tokens, and
// checksum-validated national IDs of Germany, France, Poland, and the CIS.
// It only recognizes; internal/mask decides what to replace.
package detect

import (
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind names what a detected value is. It only affects normalization.
type Kind string

// The kinds, in no particular order; priority is the order of the
// detectors table below.
const (
	Email            Kind = "email"
	Phone            Kind = "phone"
	IPv4             Kind = "ipv4"
	IPv6             Kind = "ipv6"
	MAC              Kind = "mac"
	Card             Kind = "card"
	IBAN             Kind = "iban"
	JWT              Kind = "jwt"
	Token            Kind = "token"
	EUVAT            Kind = "eu_vat"
	DESteuerID       Kind = "de_steuer_id"
	DERVNR           Kind = "de_rvnr"
	DEIDCard         Kind = "de_id_card"
	DEKVNR           Kind = "de_kvnr"
	FRNIR            Kind = "fr_nir"
	PLPesel          Kind = "pl_pesel"
	PLNIP            Kind = "pl_nip"
	PLIDCard         Kind = "pl_id_card"
	PLPassport       Kind = "pl_passport"
	RUINN            Kind = "ru_inn"
	RUSNILS          Kind = "ru_snils"
	RUOGRNIP         Kind = "ru_ogrnip"
	RUOMS            Kind = "ru_oms"
	KZIIN            Kind = "kz_iin"
	BYPersonalNumber Kind = "by_personal_number"
	UARNOKPP         Kind = "ua_rnokpp"
	UAUNZR           Kind = "ua_unzr"
	UZPINFL          Kind = "uz_pinfl"
)

// Span is one detected value inside a text, as byte offsets with End
// exclusive.
type Span struct {
	Start, End int
	Kind       Kind
}

// detector recognizes one kind.
// match judges a whole trimmed value; each finder proposes candidate byte
// ranges in a text, which Find then checks for word boundaries and with
// match. rawMatch is match without prefilters; the tests compare the two.
type detector struct {
	kind     Kind
	match    func(s string) bool
	rawMatch func(s string) bool
	finders  []finder
}

// shaped builds a detector from regular-expression shapes (the textual
// forms the kind may take) and a validator (checksum or structure).
// Each shape gets the prefilter derived from it, for Match and for Find.
func shaped(kind Kind, valid func(string) bool, shapes ...string) detector {
	type full struct {
		re *regexp.Regexp
		pf prefilter
	}
	var fulls []full
	var finders []finder
	for _, sh := range shapes {
		re, pf := regexp.MustCompile(sh), derivePrefilter(sh)
		fulls = append(fulls, full{regexp.MustCompile(`^(?:` + sh + `)$`), pf})
		finders = append(finders, finder{pf: pf, find: func(text string) [][2]int {
			var out [][2]int
			for _, loc := range re.FindAllStringIndex(text, -1) {
				out = append(out, [2]int{loc[0], loc[1]})
			}
			return out
		}})
	}
	// The first shape that matches decides, as without prefilters: a shape
	// the prefilter rejects cannot match, so skipping it changes nothing.
	match := func(s string, filtered bool) bool {
		for i := range fulls {
			if filtered && !fulls[i].pf.admits(s) {
				continue
			}
			if fulls[i].re.MatchString(s) {
				return valid(s)
			}
		}
		return false
	}
	return detector{
		kind:     kind,
		match:    func(s string) bool { return match(s, true) },
		rawMatch: func(s string) bool { return match(s, false) },
		finders:  finders,
	}
}

// candidateFinder is the finder of a detector whose find proposes ranges
// only inside the matches of cand.
func candidateFinder(cand *regexp.Regexp, find func(text string) [][2]int) []finder {
	return []finder{{pf: derivePrefilter(cand.String()), find: find}}
}

// detectors is the fixed priority order: when several kinds match the
// same text, the earliest one here is reported (so a Luhn-valid 16-digit
// number is a card, never an ru_oms, and a phone beats bare-digit IDs).
// The order is: values with a distinctive text structure (email, jwt,
// token, iban, eu_vat, ipv6, ipv4, mac, phone, card), then IDs with
// letters or separators, then bare-digit IDs from longest to shortest.
// In Find, overlapping candidates are resolved first by length (longer
// wins), then by this order.
var detectors = []detector{
	emailDetector(),
	jwtDetector(),
	tokenDetector(),
	ibanDetector(),
	euVATDetector(),
	ipv6Detector(),
	ipv4Detector(),
	macDetector(),
	phoneDetector(),
	cardDetector(),
	deRVNRDetector(),
	deIDCardDetector(),
	deKVNRDetector(),
	frNIRDetector(),
	byPersonalNumberDetector(),
	plIDCardDetector(),
	plPassportDetector(),
	uaUNZRDetector(),
	ruOGRNIPDetector(), // 15 digits
	ruOMSDetector(),    // 16 digits (shadowed by card, see its comment)
	uzPINFLDetector(),  // 14 digits
	ruINNDetector(),    // 12 (and 10) digits
	kzIINDetector(),    // 12 digits
	ruSNILSDetector(),  // 11 digits
	plPeselDetector(),  // 11 digits
	deSteuerIDDetector(),
	plNIPDetector(),    // 10 digits
	uaRNOKPPDetector(), // 10 digits
}

// Match reports whether the whole trimmed s is one detected value, and
// of which kind.
func Match(s string) (Kind, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	for _, d := range detectors {
		if d.match(s) {
			return d.kind, true
		}
	}
	return "", false
}

type candidate struct {
	Span
	prio int
}

// finderRef is one finder of the detector at priority prio.
type finderRef struct {
	prio int
	f    *finder
}

// finderGroup is the finders that share one prefilter alphabet, so the
// runs of that alphabet are computed once per text for all of them.
type finderGroup struct {
	alpha   *byteSet // nil: the finders run on the whole text
	members []finderRef
}

var finderGroups = groupFinders(detectors)

func groupFinders(ds []detector) []finderGroup {
	var groups []finderGroup
	index := map[byteSet]int{}
	for prio := range ds {
		for i := range ds[prio].finders {
			f := &ds[prio].finders[i]
			ref := finderRef{prio, f}
			if f.pf.alpha == nil {
				groups = append(groups, finderGroup{nil, []finderRef{ref}})
				continue
			}
			g, ok := index[*f.pf.alpha]
			if !ok {
				g = len(groups)
				index[*f.pf.alpha] = g
				groups = append(groups, finderGroup{alpha: f.pf.alpha})
			}
			groups[g].members = append(groups[g].members, ref)
		}
	}
	return groups
}

// Find returns every detected value inside text, sorted by Start,
// non-overlapping, and bounded by non-word characters.
func Find(text string) []Span {
	var cands []candidate
	add := func(prio int, loc [2]int) {
		d := &detectors[prio]
		s, e := loc[0], loc[1]
		if e <= s || !bounded(text, s, e) || !d.match(text[s:e]) {
			return
		}
		cands = append(cands, candidate{Span{s, e, d.kind}, prio})
	}
	for _, g := range finderGroups {
		if g.alpha == nil {
			for _, m := range g.members {
				for _, loc := range m.f.find(text) {
					add(m.prio, loc)
				}
			}
			continue
		}
		for i := 0; i < len(text); {
			if !g.alpha[text[i]] {
				i++
				continue
			}
			j := i + 1
			for j < len(text) && g.alpha[text[j]] {
				j++
			}
			run := text[i:j]
			for _, m := range g.members {
				if !m.f.pf.runAdmits(run) {
					continue
				}
				for _, loc := range m.f.find(run) {
					add(m.prio, [2]int{loc[0] + i, loc[1] + i})
				}
			}
			i = j
		}
	}
	return resolve(cands)
}

// resolve orders the candidates and keeps the non-overlapping ones: longer
// first, then by detector priority, then by position. Two candidates equal
// in all three are the same span, so the order the finders proposed them
// in does not matter.
func resolve(cands []candidate) []Span {
	sort.SliceStable(cands, func(i, j int) bool {
		li, lj := cands[i].End-cands[i].Start, cands[j].End-cands[j].Start
		if li != lj {
			return li > lj
		}
		if cands[i].prio != cands[j].prio {
			return cands[i].prio < cands[j].prio
		}
		return cands[i].Start < cands[j].Start
	})
	var out []Span
	for _, c := range cands {
		overlap := false
		for _, o := range out {
			if c.Start < o.End && o.Start < c.End {
				overlap = true
				break
			}
		}
		if !overlap {
			out = append(out, c.Span)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

// bounded reports whether text[start:end] stands alone: no word character
// touches it, it is not the tail of an '@' or '#' token, and it is not a
// piece of a longer dash, dot, or slash joined token (a UUID segment, a
// dotted version) or of a number with thousands commas.
func bounded(text string, start, end int) bool {
	first, last := text[start], text[end-1]
	if start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if isWord(r) || r == '@' || r == '#' {
			return false
		}
		if start-size > 0 {
			p, _ := utf8.DecodeLastRuneInString(text[:start-size])
			switch {
			case r == '-' || r == '.' || r == '/':
				if isWord(p) {
					return false
				}
			case r == ',':
				if isDigitByte(first) && p < utf8.RuneSelf && isDigitByte(byte(p)) {
					return false
				}
			}
		}
	}
	if end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if isWord(r) || r == '@' {
			return false
		}
		if end+size < len(text) {
			n, _ := utf8.DecodeRuneInString(text[end+size:])
			switch {
			case r == '-' || r == '.' || r == '/':
				if isWord(n) {
					return false
				}
			case r == ',':
				if isDigitByte(last) && n < utf8.RuneSelf && isDigitByte(byte(n)) {
					return false
				}
			}
		}
	}
	return true
}

// Normalize returns the canonical form of a value of kind k, used before
// hashing so that differently written equal values share a Marker.
func Normalize(k Kind, s string) string {
	s = strings.TrimSpace(s)
	switch k {
	case Email:
		return strings.ToLower(s)
	case Phone:
		if e, ok := phoneE164(s); ok {
			return e
		}
		return s
	case IPv4, IPv6:
		if a, err := netip.ParseAddr(s); err == nil {
			return a.String()
		}
		return s
	case MAC:
		return strings.ToLower(strings.ReplaceAll(s, "-", ":"))
	case JWT, Token:
		return s
	case BYPersonalNumber:
		return latinLookalikes.Replace(strings.ToUpper(clean(s)))
	default:
		return strings.ToUpper(clean(s))
	}
}

// clean removes the separators that IDs are written with: spaces,
// dashes, dots, and slashes.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '/':
			return -1
		}
		return r
	}, s)
}
