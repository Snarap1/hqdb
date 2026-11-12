package detect

import (
	"regexp"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

// Phone policy. A value is a phone only when libphonenumber says it is a
// valid number and it carries enough evidence of being a phone rather
// than an ID, a date, or a counter:
//   - "+" followed by the international number: any region, any type,
//     8 to 15 digits;
//   - "00" followed by the international number: same, but digits without
//     any separator must be a mobile number;
//   - national format for DE, FR, UA (trunk prefix 0) and RU, KZ, BY, UZ
//     (trunk prefix 8), at least 9 digits including the trunk prefix; digits
//     without any separator (space, dash, parenthesis) must be a mobile
//     number, so a bare 10 or 11 digit ID is not taken for a landline;
//   - national PL has no trunk prefix, so it is accepted only written as
//     NNN NNN NNN, NNN-NNN-NNN, or NN NNN NN NN.
//
// Bare digit strings with no prefix at all (12345678, 4915112345678) are
// never phones; a phone stored as a number must carry its "+" to be found.
// Date-shaped strings (08-12-2024) are rejected.
var phoneTrunk = []struct{ region, trunk string }{
	{"DE", "0"}, {"FR", "0"}, {"UA", "0"},
	{"RU", "8"}, {"KZ", "8"}, {"BY", "8"}, {"UZ", "8"},
}

var (
	plPhoneShape  = regexp.MustCompile(`^(?:\d{3}[ -]\d{3}[ -]\d{3}|\d{2} \d{3} \d{2} \d{2})$`)
	dateLikePhone = regexp.MustCompile(`^\d{2}[ -]\d{2}[ -]\d{4}$`)
	phoneCand     = regexp.MustCompile(`[+(]?\d[\d ()\-]*\d`)
)

var (
	frPhoneShape = regexp.MustCompile(`^0\d( \d\d){4}$`)
	uaPhoneShape = regexp.MustCompile(`^\(?0\d{2}\)? ?\d{3}[ -]\d{2}[ -]\d{2}$`)
)

// orderedRegions lists the national regions to try. A national number can
// be valid in several regions (06 12 34 56 78 is a German landline and a
// French mobile), so the written grouping decides which region is tried
// first: five groups of two digits is French, 0NN NNN NN NN is Ukrainian.
func orderedRegions(s string) []struct{ region, trunk string } {
	first := ""
	switch {
	case frPhoneShape.MatchString(s):
		first = "FR"
	case uaPhoneShape.MatchString(s):
		first = "UA"
	}
	if first == "" {
		return phoneTrunk
	}
	out := make([]struct{ region, trunk string }, 0, len(phoneTrunk))
	for _, r := range phoneTrunk {
		if r.region == first {
			out = append(out, r)
		}
	}
	for _, r := range phoneTrunk {
		if r.region != first {
			out = append(out, r)
		}
	}
	return out
}

func phoneDetector() detector {
	return detector{
		kind: Phone,
		match: func(s string) bool {
			_, ok := phoneE164(s)
			return ok
		},
		find: phoneFind,
	}
}

// phoneFind proposes every window of separator-joined digit groups inside
// each phone-looking run, so "call 030 1234567 12" can still find the
// number inside a longer run; Find keeps the longest valid, non-overlapping
// windows.
func phoneFind(text string) [][2]int {
	var out [][2]int
	for _, loc := range phoneCand.FindAllStringIndex(text, -1) {
		cand := text[loc[0]:loc[1]]
		if countDigits(cand) < 8 {
			continue
		}
		starts, ends := []int{0}, []int{len(cand)}
		for i := 0; i < len(cand); i++ {
			if cand[i] == ' ' || cand[i] == '-' {
				ends = append(ends, i)
				starts = append(starts, i+1)
			}
		}
		if len(starts)*len(ends) > 400 {
			continue
		}
		for _, st := range starts {
			for _, en := range ends {
				if en <= st || cand[st] == ' ' || cand[st] == '-' || cand[en-1] == ' ' || cand[en-1] == '-' {
					continue
				}
				out = append(out, [2]int{loc[0] + st, loc[0] + en})
			}
		}
	}
	return out
}

func countDigits(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if isDigitByte(s[i]) {
			n++
		}
	}
	return n
}

// phoneE164 applies the phone policy and returns the E.164 form.
func phoneE164(s string) (string, bool) {
	s = strings.TrimSpace(s)
	plus, hasSep, n := false, false, 0
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			n++
		case r == '+' && i == 0:
			plus = true
		case r == ' ' || r == '-' || r == '(' || r == ')':
			hasSep = true
		default:
			return "", false
		}
	}
	if n < 8 || n > 15 || strings.Count(s, "(") != strings.Count(s, ")") ||
		strings.Contains(s, "  ") || strings.Contains(s, "--") {
		return "", false
	}
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)

	if plus {
		return validE164(s, "", false)
	}
	if strings.HasPrefix(d, "00") {
		return validE164("+"+d[2:], "", !hasSep)
	}
	if n < 9 || dateLikePhone.MatchString(s) {
		return "", false
	}
	if n == 9 && plPhoneShape.MatchString(s) {
		return validE164(d, "PL", false)
	}
	for _, r := range orderedRegions(s) {
		if !strings.HasPrefix(d, r.trunk) {
			continue
		}
		if e, ok := validE164(d, r.region, !hasSep); ok {
			return e, true
		}
	}
	return "", false
}

// validE164 parses s and returns the E.164 form when libphonenumber finds
// the number valid; mobileOnly additionally requires a mobile number.
func validE164(s, region string, mobileOnly bool) (string, bool) {
	num, err := phonenumbers.Parse(s, region)
	if err != nil || !phonenumbers.IsValidNumber(num) {
		return "", false
	}
	if mobileOnly {
		t := phonenumbers.GetNumberType(num)
		if t != phonenumbers.MOBILE && t != phonenumbers.FIXED_LINE_OR_MOBILE {
			return "", false
		}
	}
	return phonenumbers.Format(num, phonenumbers.E164), true
}
