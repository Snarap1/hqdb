package detect

import (
	"strconv"
	"strings"
	"time"
)

// Shared helpers.

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigitByte(s[i]) {
			return false
		}
	}
	return true
}

// num parses a digit string; callers have checked isDigits.
func num(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func luhn(d string) bool {
	sum := 0
	for i, alt := 0, false; i < len(d); i++ {
		v := int(d[len(d)-1-i] - '0')
		if alt {
			v *= 2
			if v > 9 {
				v -= 9
			}
		}
		sum += v
		alt = !alt
	}
	return sum%10 == 0
}

// iso7064 is ISO 7064 MOD 11,10 as used by the German tax ID and VAT
// number: it returns the check digit for the digits d.
func iso7064(d string) int {
	product := 10
	for i := 0; i < len(d); i++ {
		sum := (int(d[i]-'0') + product) % 10
		if sum == 0 {
			sum = 10
		}
		product = (sum * 2) % 11
	}
	check := 11 - product
	if check == 10 {
		check = 0
	}
	return check
}

func validDate(y, m, d int) bool {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Year() == y && int(t.Month()) == m && t.Day() == d
}

// validDM checks day and month when the year is not known (February may
// have 29 days).
func validDM(d, m int) bool {
	days := [...]int{31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	return m >= 1 && m <= 12 && d >= 1 && d <= days[m-1]
}

// weighted returns the sum of weights[i] * value(d[i]), where a digit is
// its value and a letter A..Z is 10..35.
func weighted(d string, weights []int) int {
	sum := 0
	for i := 0; i < len(d) && i < len(weights); i++ {
		sum += weights[i] * charValue(d[i])
	}
	return sum
}

func charValue(c byte) int {
	if c >= 'A' && c <= 'Z' {
		return int(c-'A') + 10
	}
	return int(c - '0')
}

func digitSum(n int) int {
	s := 0
	for ; n > 0; n /= 10 {
		s += n % 10
	}
	return s
}

// century returns the base year for the sex/century digit of the Kazakh
// IIN: 1, 2 for the 1800s,
// 3, 4 for the 1900s, 5, 6 for the 2000s.
func century(c byte) int {
	switch c {
	case '1', '2':
		return 1800
	case '3', '4':
		return 1900
	case '5', '6':
		return 2000
	}
	return 0
}

func alphaOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return s != ""
}

// Germany

func deSteuerIDDetector() detector {
	return shaped(DESteuerID, validSteuerID, `\d{11}`, `\d{2} \d{3} \d{3} \d{3}`)
}

// validSteuerID: 11 digits, first not 0, among the first ten exactly one
// digit occurs two or three times (never three in a row), check digit by
// ISO 7064 MOD 11,10.
func validSteuerID(s string) bool {
	d := clean(s)
	if len(d) != 11 || !isDigits(d) || d[0] == '0' {
		return false
	}
	var counts [10]int
	for i := 0; i < 10; i++ {
		counts[d[i]-'0']++
	}
	repeated := 0
	for _, c := range counts {
		if c > 3 {
			return false
		}
		if c >= 2 {
			repeated++
		}
	}
	if repeated != 1 {
		return false
	}
	for i := 0; i+2 < 10; i++ {
		if d[i] == d[i+1] && d[i+1] == d[i+2] {
			return false
		}
	}
	return iso7064(d[:10]) == int(d[10]-'0')
}

func deRVNRDetector() detector {
	return shaped(DERVNR, validRVNR, `\d{8}[A-Z]\d{3}`, `\d{2} \d{6} [A-Z] \d{3}`)
}

// validRVNR: area (2 digits), birth date DDMMYY, initial of the birth
// surname, serial (2 digits), check digit. The letter counts as its two
// digit alphabet position; weights 2 1 2 5 7 1 2 1 2 1 2 1, cross sums of
// the products, sum mod 10.
func validRVNR(s string) bool {
	c := clean(s)
	if len(c) != 12 || !isDigits(c[:8]) || c[8] < 'A' || c[8] > 'Z' || !isDigits(c[9:]) {
		return false
	}
	if !validDM(num(c[2:4]), num(c[4:6])) {
		return false
	}
	digits := c[:8] + strconv.Itoa(int(c[8]-'A') + 101)[1:] + c[9:11]
	weights := [...]int{2, 1, 2, 5, 7, 1, 2, 1, 2, 1, 2, 1}
	sum := 0
	for i := 0; i < 12; i++ {
		sum += digitSum(int(digits[i]-'0') * weights[i])
	}
	return sum%10 == int(c[11]-'0')
}

const deIDAlphabet = "CFGHJKLMNPRTVWXYZ0123456789"

func deIDCardDetector() detector {
	return shaped(DEIDCard, validDEIDCard, `[CFGHJKLMNPRTVWXYZ0-9]{9}\d`)
}

// validDEIDCard: the nine character serial of the Personalausweis plus
// the ICAO 9303 check digit (weights 7 3 1). The serial must mix letters
// and digits, so bare 10-digit numbers are never ID cards.
func validDEIDCard(s string) bool {
	c := clean(s)
	if len(c) != 10 || !isDigitByte(c[9]) {
		return false
	}
	letters, digits := 0, 0
	for i := 0; i < 9; i++ {
		if !strings.ContainsRune(deIDAlphabet, rune(c[i])) {
			return false
		}
		if isDigitByte(c[i]) {
			digits++
		} else {
			letters++
		}
	}
	if letters == 0 || digits == 0 {
		return false
	}
	return weighted(c[:9], []int{7, 3, 1, 7, 3, 1, 7, 3, 1})%10 == int(c[9]-'0')
}

func deKVNRDetector() detector {
	return shaped(DEKVNR, validKVNR, `[A-Z]\d{9}`)
}

// validKVNR: letter plus 9 digits; the letter counts as its two digit
// alphabet position, digits are weighted 1 2 1 2 ..., cross sums added,
// sum mod 10 is the last digit.
func validKVNR(s string) bool {
	c := clean(s)
	if len(c) != 10 || c[0] < 'A' || c[0] > 'Z' || !isDigits(c[1:]) {
		return false
	}
	digits := strconv.Itoa(int(c[0]-'A') + 101)[1:] + c[1:9]
	sum := 0
	for i := 0; i < 10; i++ {
		w := 1 + i%2
		sum += digitSum(int(digits[i]-'0') * w)
	}
	return sum%10 == int(c[9]-'0')
}

// France

func frNIRDetector() detector {
	return shaped(FRNIR, validNIR,
		`[123478]\d{4}(?:\d{2}|2[AB])\d{8}`,
		`[123478] \d{2} \d{2} (?:\d{2}|2[AB]) \d{3} \d{3} \d{2}`)
}

// validNIR: sex digit (1, 2; the rare historical 3, 4; 7, 8 for
// temporary numbers), birth year and
// month (01 to 12, or 20, 30 to 42, 50 and above when the month is
// unknown), department (Corsica 2A and 2B count as 19 and 18), commune,
// order number, and the key 97 - (first 13 digits mod 97).
func validNIR(s string) bool {
	c := clean(s)
	if len(c) != 15 || !strings.ContainsRune("123478", rune(c[0])) || !isDigits(c[1:5]) {
		return false
	}
	m := num(c[3:5])
	if !(m >= 1 && m <= 12 || m == 20 || m >= 30 && m <= 42 || m >= 50) {
		return false
	}
	dept := c[5:7]
	switch {
	case dept == "2A":
		dept = "19"
	case dept == "2B":
		dept = "18"
	case isDigits(dept):
		if d := num(dept); d < 1 || d > 95 && d < 97 {
			return false
		}
	default:
		return false
	}
	body := c[:5] + dept + c[7:13]
	if !isDigits(body) || !isDigits(c[13:]) {
		return false
	}
	n, err := strconv.ParseUint(body, 10, 64)
	if err != nil {
		return false
	}
	return uint64(num(c[13:])) == 97-n%97
}

// Poland

func plPeselDetector() detector {
	return shaped(PLPesel, validPesel, `\d{11}`)
}

// validPesel: weights 1 3 7 9 1 3 7 9 1 3, check digit (10 - sum mod 10)
// mod 10, valid birth date with the century encoded in the month
// (+80 for 1800s, +20 for 2000s, +40 for 2100s, +60 for 2200s).
func validPesel(s string) bool {
	if len(s) != 11 || !isDigits(s) {
		return false
	}
	w := [...]int{1, 3, 7, 9, 1, 3, 7, 9, 1, 3}
	sum := 0
	for i, x := range w {
		sum += x * int(s[i]-'0')
	}
	if (10-sum%10)%10 != int(s[10]-'0') {
		return false
	}
	month := num(s[2:4])
	year := 1900 + num(s[0:2])
	switch month / 20 {
	case 0:
	case 1:
		year += 100
	case 2:
		year += 200
	case 3:
		year += 300
	case 4:
		year -= 100
	default:
		return false
	}
	return validDate(year, month%20, num(s[4:6]))
}

func plNIPDetector() detector {
	return shaped(PLNIP, validNIP,
		`\d{10}`,
		`\d{3}-\d{3}-\d{2}-\d{2}`,
		`\d{3}-\d{2}-\d{2}-\d{3}`,
		`\d{3} \d{3} \d{2} \d{2}`)
}

// validNIP: weights 6 5 7 2 3 4 5 6 7, sum mod 11 is the check digit
// (10 is invalid).
func validNIP(s string) bool {
	d := clean(s)
	if len(d) != 10 || !isDigits(d) || d[:3] == "000" {
		return false
	}
	sum := 0
	for i, w := range []int{6, 5, 7, 2, 3, 4, 5, 6, 7} {
		sum += w * int(d[i]-'0')
	}
	return sum%11 == int(d[9]-'0')
}

func plIDCardDetector() detector {
	return shaped(PLIDCard, validPLIDCard, `[A-Z]{3}\d{6}`)
}

// validPLIDCard: 3 series letters then 6 digits; the first digit is the
// check digit of weights 7 3 1 0 7 3 1 7 3 (A = 10).
func validPLIDCard(s string) bool {
	if len(s) != 9 || !alphaOnly(s[:3]) || !isDigits(s[3:]) {
		return false
	}
	return weighted(s, []int{7, 3, 1, 0, 7, 3, 1, 7, 3})%10 == int(s[3]-'0')
}

func plPassportDetector() detector {
	return shaped(PLPassport, validPLPassport, `[A-Z]{2}\d{7}`)
}

// validPLPassport: 2 letters then 7 digits; the first digit is the check
// digit; with weights 7 3 9 1 7 3 1 7 3 (A = 10) the whole sum is 0 mod 10
// (pl.wikipedia.org "Paszporty w Polsce", worked example CC7999486).
func validPLPassport(s string) bool {
	if len(s) != 9 || !alphaOnly(s[:2]) || !isDigits(s[2:]) {
		return false
	}
	return weighted(s, []int{7, 3, 9, 1, 7, 3, 1, 7, 3})%10 == 0
}

// Russia

func ruINNDetector() detector {
	return shaped(RUINN, validINN, `\d{10}`, `\d{12}`)
}

func validINN(s string) bool {
	if !isDigits(s) {
		return false
	}
	check := func(w []int, n int) bool {
		return (weighted(s, w)%11)%10 == int(s[n]-'0')
	}
	switch len(s) {
	case 10:
		return check([]int{2, 4, 10, 3, 5, 9, 4, 6, 8}, 9)
	case 12:
		return check([]int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}, 10) &&
			check([]int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}, 11)
	}
	return false
}

func ruSNILSDetector() detector {
	return shaped(RUSNILS, validSNILS, `\d{11}`, `\d{3}-\d{3}-\d{3}[ -]\d{2}`, `\d{3} \d{3} \d{3} \d{2}`)
}

// validSNILS: 9 digits weighted 9..1, check number 0 for sums 100 and 101,
// sum mod 101 above that. Numbers up to 001-001-998 are not checksummed
// by the issuer and are not recognized.
func validSNILS(s string) bool {
	d := clean(s)
	if len(d) != 11 || !isDigits(d) || num(d[:9]) <= 1001998 {
		return false
	}
	sum := 0
	for i := 0; i < 9; i++ {
		sum += (9 - i) * int(d[i]-'0')
	}
	if sum > 101 {
		sum %= 101
	}
	if sum >= 100 {
		sum = 0
	}
	return sum == num(d[9:])
}

func ruOGRNIPDetector() detector {
	return shaped(RUOGRNIP, validOGRNIP, `[34]\d{14}`)
}

// validOGRNIP: 15 digits, the last is (first 14 digits mod 13) mod 10.
func validOGRNIP(s string) bool {
	if len(s) != 15 || !isDigits(s) {
		return false
	}
	n, _ := strconv.ParseInt(s[:14], 10, 64)
	return int(n%13%10) == int(s[14]-'0')
}

func ruOMSDetector() detector {
	return shaped(RUOMS, validOMS, `\d{16}`)
}

// validOMS: 16 digits with the ISO/HL7 27931 Mod10 check digit that FOMS
// order No. 276 of 26 December 2013 prescribes, which equals Luhn (see
// TestOMSIsHL7Mod10). Every such number is also a Luhn-valid 16-digit
// number, so Card, which has priority, takes all of them whose first digit
// is a card prefix; ru_oms catches the rest (first digit 0, 1, 7, 8, 9).
func validOMS(s string) bool {
	return len(s) == 16 && isDigits(s) && strings.Count(s, s[:1]) != 16 && luhn(s)
}

// Kazakhstan

func kzIINDetector() detector {
	return shaped(KZIIN, validIIN, `\d{12}`)
}

// validIIN: YYMMDD, a century/sex digit 1 to 6, 4 more digits, and a check
// digit: weights 1..11 mod 11, or 3..11 1 2 when that gives 10 (Rules for
// forming the IIN, order registered as No. 32942 in 2023); 10 again is
// never issued. The current rules no longer promise that an IIN starts
// with the birth date, but almost every IIN issued does; requiring the
// date keeps plain 12-digit numbers such as UPC codes from matching
// (one in eleven passes the checksum alone), so an IIN that is not built
// from a birth date is caught by column name only.
func validIIN(s string) bool {
	if len(s) != 12 || !isDigits(s) || s[6] < '1' || s[6] > '6' {
		return false
	}
	if !validDate(century(s[6])+num(s[0:2]), num(s[2:4]), num(s[4:6])) {
		return false
	}
	c := weighted(s[:11], []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}) % 11
	if c == 10 {
		c = weighted(s[:11], []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 1, 2}) % 11
	}
	return c != 10 && c == int(s[11]-'0')
}

// Belarus

// byLetter is a letter of the Belarusian personal number, Latin or the
// Cyrillic look-alike people type for it.
const byLetter = `[A-ZАВЕКМНОРСТХ]`

func byPersonalNumberDetector() detector {
	return shaped(BYPersonalNumber, validBYPersonal, `[1-9]\d{6}`+byLetter+`\d{3}`+byLetter+`{2}\d`)
}

// latinLookalikes maps the Cyrillic capitals that look like Latin ones to
// those Latin letters.
var latinLookalikes = strings.NewReplacer(
	"А", "A", "В", "B", "Е", "E", "К", "K", "М", "M", "Н", "H",
	"О", "O", "Р", "P", "С", "C", "Т", "T", "Х", "X")

// validBYPersonal: a digit, 6 digits, a letter, 3 digits, 2 letters, and a
// check digit computed modulo 10 with the repeating weights 7 3 1 over the
// first 13 characters, letters A..Z counting 10..35 (MVD resolution
// No. 345 of 18 October 2011). Before 2012 the first digit was the
// century and sex (1 to 6) and the next six the birth date; numbers
// issued since are not built from the birth date (first digit 7 and
// above), so the date is not checked. Cyrillic look-alike letters count
// as their Latin twins.
func validBYPersonal(s string) bool {
	s = latinLookalikes.Replace(s)
	if len(s) != 14 || !isDigits(s[:7]) || s[0] == '0' || !alphaOnly(s[7:8]) ||
		!isDigits(s[8:11]) || !alphaOnly(s[11:13]) || !isDigitByte(s[13]) {
		return false
	}
	w := make([]int, 13)
	for i := range w {
		w[i] = []int{7, 3, 1}[i%3]
	}
	return weighted(s[:13], w)%10 == int(s[13]-'0')
}

// Ukraine

func uaRNOKPPDetector() detector {
	return shaped(UARNOKPP, validRNOKPP, `\d{10}`)
}

// validRNOKPP: the first five digits are the days since 1899-12-31 of the
// birth date (limited to 1..55000, i.e. up to about 2050, to keep other
// 10-digit numbers out), weights -1 5 7 9 4 6 10 5 7, (sum mod 11) mod 10
// is the last digit (python-stdnum ua.rntrc, example 1759013776).
func validRNOKPP(s string) bool {
	if len(s) != 10 || !isDigits(s) {
		return false
	}
	if days := num(s[:5]); days < 1 || days > 55000 {
		return false
	}
	sum := weighted(s[:9], []int{-1, 5, 7, 9, 4, 6, 10, 5, 7})
	return ((sum%11)+11)%11%10 == int(s[9]-'0')
}

func uaUNZRDetector() detector {
	return shaped(UAUNZR, validUNZR, `\d{8}-\d{5}`)
}

// validUNZR: YYYYMMDD-NNNNN: the birth date, a 4-digit code (odd for men,
// even for women), and a check digit by the passport MRZ method, weights
// 7 3 1 repeating over the first 12 digits, sum mod 10 (uk.wikipedia.org
// "Єдиний державний демографічний реєстр"; no published worked example).
func validUNZR(s string) bool {
	if len(s) != 14 || s[8] != '-' || !isDigits(s[:8]) || !isDigits(s[9:]) {
		return false
	}
	y := num(s[:4])
	if y < 1900 || y > 2100 || !validDate(y, num(s[4:6]), num(s[6:8])) {
		return false
	}
	d := s[:8] + s[9:]
	return weighted(d[:12], []int{7, 3, 1, 7, 3, 1, 7, 3, 1, 7, 3, 1})%10 == int(d[12]-'0')
}

// Uzbekistan

func uzPINFLDetector() detector {
	return shaped(UZPINFL, validPINFL, `[3-6]\d{13}`)
}

// validPINFL: 14 digits, a sex/century digit 3 to 6, DDMMYY, region,
// serial, check digit with weights 7 3 1 repeating over the first 13
// digits, mod 10 (no official description found; agrees with the uz_kit
// UzPinflValidator example 30101901234569).
func validPINFL(s string) bool {
	if len(s) != 14 || !isDigits(s) || s[0] < '3' || s[0] > '6' {
		return false
	}
	if !validDM(num(s[1:3]), num(s[3:5])) {
		return false
	}
	w := make([]int, 13)
	for i := range w {
		w[i] = []int{7, 3, 1}[i%3]
	}
	return weighted(s[:13], w)%10 == int(s[13]-'0')
}
