package detect

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"regexp"
	"strings"
)

func emailDetector() detector {
	return shaped(Email, validEmail,
		`[\p{L}\p{N}_][\p{L}\p{N}._%+\-]*@(?:[\p{L}\p{N}](?:[\p{L}\p{N}\-]*[\p{L}\p{N}])?\.)+\p{L}{2,}`)
}

func validEmail(s string) bool {
	at := strings.LastIndexByte(s, '@')
	if at < 1 || len(s) > 254 {
		return false
	}
	local := s[:at]
	return len(local) <= 64 && !strings.HasSuffix(local, ".") && !strings.Contains(local, "..")
}

const b64url = `[A-Za-z0-9_\-]`

func jwtDetector() detector {
	return shaped(JWT, validJWT, `eyJ`+b64url+`+\.`+b64url+`+\.`+b64url+`*`)
}

func validJWT(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[1] == "" {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[0], "="))
	if err != nil {
		return false
	}
	var header map[string]any
	if json.Unmarshal(raw, &header) != nil {
		return false
	}
	_, ok := header["alg"]
	return ok
}

func tokenDetector() detector {
	return shaped(Token, func(string) bool { return true },
		`sk-[A-Za-z0-9_\-]{20,}`,
		`sk_live_[A-Za-z0-9]{16,}`,
		`gh[po]_[A-Za-z0-9]{36,}`,
		`github_pat_[A-Za-z0-9_]{22,}`,
		`xox[bpas]-[A-Za-z0-9\-]{10,}`,
		`AKIA[0-9A-Z]{16}`,
		`AIza[0-9A-Za-z_\-]{35}`,
	)
}

// IBAN

var ibanLengths = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28, "BA": 20, "BE": 16, "BG": 22,
	"BH": 22, "BR": 29, "BY": 28, "CH": 21, "CR": 22, "CY": 28, "CZ": 24, "DE": 22,
	"DK": 18, "DO": 28, "EE": 20, "EG": 29, "ES": 24, "FI": 18, "FO": 18, "FR": 27,
	"GB": 22, "GE": 22, "GI": 23, "GL": 18, "GR": 27, "GT": 28, "HR": 21, "HU": 28,
	"IE": 22, "IL": 23, "IQ": 23, "IS": 26, "IT": 27, "JO": 30, "KW": 30, "KZ": 20,
	"LB": 28, "LC": 32, "LI": 21, "LT": 20, "LU": 20, "LV": 21, "LY": 25, "MC": 27,
	"MD": 24, "ME": 22, "MK": 19, "MR": 27, "MT": 31, "MU": 30, "NL": 18, "NO": 15,
	"PK": 24, "PL": 28, "PS": 29, "PT": 25, "QA": 29, "RO": 24, "RS": 22, "RU": 33,
	"SA": 24, "SC": 31, "SE": 24, "SI": 19, "SK": 24, "SM": 27, "ST": 25, "SV": 28,
	"TL": 23, "TN": 24, "TR": 26, "UA": 29, "VA": 22, "VG": 24, "XK": 20,
}

func ibanDetector() detector {
	return shaped(IBAN, validIBAN,
		`(?i)[A-Z]{2}\d{2}[A-Z0-9]{11,30}`,
		`(?i)[A-Z]{2}\d{2}(?: [A-Z0-9]{4}){2,7}(?: [A-Z0-9]{1,4})?`,
	)
}

func validIBAN(s string) bool {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if len(s) < 15 {
		return false
	}
	n, ok := ibanLengths[s[:2]]
	if !ok || len(s) != n {
		return false
	}
	rem := 0
	for _, r := range s[4:] + s[:4] {
		switch {
		case r >= '0' && r <= '9':
			rem = (rem*10 + int(r-'0')) % 97
		case r >= 'A' && r <= 'Z':
			rem = (rem*100 + int(r-'A') + 10) % 97
		default:
			return false
		}
	}
	return rem == 1
}

// Bank card

// Card policy: 14 to 19 digits, Luhn, not all one digit, and an issuer
// prefix plausible for payment cards (first digit 3 to 6, or 22 to 27 for
// Mir and Mastercard 2-series). The prefix rule keeps Luhn-valid epoch
// milliseconds, ID numbers, and zero-padded counters from being cards.
// 13 digits (a legacy Visa length) is left out: about one in ten plain
// 13-digit IDs starting 3 to 6 passes Luhn.
func cardDetector() detector {
	return shaped(Card, validCard,
		`\d{14,19}`,
		`\d{4}[ -]\d{4}[ -]\d{4}[ -]\d{1,7}`,
		`\d{4}[ -]\d{6}[ -]\d{5}`,
		`\d{4}[ -]\d{6}[ -]\d{4}`,
	)
}

var cardSeparators = strings.NewReplacer(" ", "", "-", "")

func validCard(s string) bool {
	d := cardSeparators.Replace(s)
	if len(d) < 14 || len(d) > 19 || !isDigits(d) {
		return false
	}
	switch {
	case d[0] >= '3' && d[0] <= '6':
	case d[0] == '2' && d[1] >= '2' && d[1] <= '7':
	default:
		return false
	}
	if strings.Count(d, d[:1]) == len(d) {
		return false
	}
	return luhn(d)
}

// Network

func ipv4Detector() detector {
	return shaped(IPv4, func(s string) bool {
		a, err := netip.ParseAddr(s)
		return err == nil && a.Is4()
	}, `\d{1,3}(?:\.\d{1,3}){3}`)
}

func validIPv6(s string) bool {
	if len(s) < 3 || strings.Contains(s, "%") || !strings.ContainsAny(s, "0123456789abcdefABCDEF") {
		return false
	}
	a, err := netip.ParseAddr(s)
	return err == nil && a.Is6()
}

var ipv6Cand = regexp.MustCompile(`[0-9A-Fa-f.]*(?::[0-9A-Fa-f.]*){2,}`)

func ipv6Detector() detector {
	return detector{
		kind:     IPv6,
		match:    validIPv6,
		rawMatch: validIPv6,
		finders: candidateFinder(ipv6Cand, func(text string) [][2]int {
			var out [][2]int
			for _, loc := range ipv6Cand.FindAllStringIndex(text, -1) {
				s, e := loc[0], loc[1]
				// a sentence may end with ':' or '.' glued to the address
				for e > s && !validIPv6(text[s:e]) && (text[e-1] == ':' || text[e-1] == '.') {
					e--
				}
				if e > s && validIPv6(text[s:e]) {
					out = append(out, [2]int{s, e})
				}
			}
			return out
		}),
	}
}

func macDetector() detector {
	return shaped(MAC, func(string) bool { return true },
		`[0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5}`,
		`[0-9A-Fa-f]{2}(?:-[0-9A-Fa-f]{2}){5}`,
	)
}

// EU VAT: DE, FR (numeric key), PL.

func euVATDetector() detector {
	return shaped(EUVAT, validVAT,
		`(?i)DE ?\d{9}`,
		`(?i)FR ?\d{2} ?\d{9}`,
		`(?i)FR ?\d{2} ?\d{3} ?\d{3} ?\d{3}`,
		`(?i)PL ?\d{10}`,
		`(?i)PL ?\d{3}[- ]?\d{3}[- ]?\d{2}[- ]?\d{2}`,
	)
}

func validVAT(s string) bool {
	c := strings.ToUpper(clean(s))
	if len(c) < 4 {
		return false
	}
	body := c[2:]
	if !isDigits(body) {
		return false
	}
	switch c[:2] {
	case "DE":
		return len(body) == 9 && body[0] != '0' && iso7064(body[:8]) == int(body[8]-'0')
	case "FR":
		if len(body) != 11 {
			return false
		}
		return num(body[:2]) == (12+3*(num(body[2:])%97))%97
	case "PL":
		return validNIP(body)
	}
	return false
}
