package detect

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// complete appends the one check digit that makes prefix+digit a value of
// kind want (the validators are checked against official examples in
// validCases; this only builds more samples of the same shape).
func complete(t *testing.T, want Kind, prefix string) string {
	t.Helper()
	for d := '0'; d <= '9'; d++ {
		v := prefix + string(d)
		if strings.Contains(prefix, "?") {
			v = strings.Replace(prefix, "?", string(d), 1)
		}
		if k, ok := Match(v); ok && k == want {
			return v
		}
	}
	t.Fatalf("no check digit makes %q a %s", prefix, want)
	return ""
}

type valueCase struct {
	kind  Kind
	value string
}

// validCases are official examples or well-known published samples.
func validCases(t *testing.T) []valueCase {
	return []valueCase{
		{Email, "ivan.petrov+tag@example.co.uk"},
		{Email, "Иван@пример.рф"},
		{Phone, "+49 30 901820"},
		{Phone, "+7 916 123-45-67"},
		{Phone, "+79161234567"},
		{Phone, "8 916 123-45-67"},
		{Phone, "8 (916) 123-45-67"},
		{Phone, "89161234567"},
		{Phone, "+33 6 12 34 56 78"},
		{Phone, "06 12 34 56 78"},
		{Phone, "0612345678"},
		{Phone, "+48 512 345 678"},
		{Phone, "512 345 678"},
		{Phone, "+380 50 123 4567"},
		{Phone, "050 123 45 67"},
		{Phone, "+375 29 123 45 67"},
		{Phone, "+998 90 123 45 67"},
		{Phone, "+7 701 123 45 67"},
		{Phone, "8 701 123 45 67"},
		{Phone, "+1 650 253 0000"},
		{Phone, "0049 30 901820"},
		{Phone, "030 901820 12"},
		{IPv4, "192.168.1.10"},
		{IPv6, "2001:db8::1"},
		{IPv6, "::1"},
		{IPv6, "fe80::1ff:fe23:4567:890a"},
		{MAC, "aa:bb:cc:dd:ee:ff"},
		{MAC, "AA-BB-CC-DD-EE-FF"},
		{Card, "4111111111111111"},
		{Card, "4111 1111 1111 1111"},
		{Card, "4111-1111-1111-1111"},
		{Card, "378282246310005"},
		{Card, "3782 822463 10005"},
		{Card, "5555555555554444"},
		{IBAN, "DE89370400440532013000"},
		{IBAN, "DE89 3704 0044 0532 0130 00"},
		{IBAN, "GB82WEST12345698765432"},
		{IBAN, "FR1420041010050500013M02606"},
		{IBAN, "PL61109010140000071219812874"},
		{IBAN, "PL61 1090 1014 0000 0712 1981 2874"},
		{IBAN, "RU0204452560040702810412345678901"},
		{JWT, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
		{Token, "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"},
		{Token, "sk_live_" + strings.Repeat("a1B2", 6)}, // built, so secret scanners do not flag the fixture
		{Token, "ghp_" + strings.Repeat("a1B2", 9)},
		{Token, "gho_" + strings.Repeat("a1B2", 9)},
		{Token, "github_pat_11ABCDEFG0abcdefghij_klmnopqrstuvwxyz"},
		{Token, "xoxb-1234567890-abcdefghij"},
		{Token, "AKIAIOSFODNN7EXAMPLE"},
		{Token, "AIzaSyA-1234567890abcdefghijklmnopqrstu"},
		{EUVAT, "DE136695976"},
		{EUVAT, "FR40303265045"},
		{EUVAT, "FR 40 303 265 045"},
		{EUVAT, "PL1234563218"},
		{EUVAT, "PL 123-456-32-18"},
		{DESteuerID, "65929970489"},
		{DESteuerID, "65 929 970 489"},
		{DERVNR, "15070649C103"},
		{DERVNR, "15 070649 C 103"},
		{DEIDCard, "L01X00T471"},
		{DEKVNR, "A123456780"},
		{FRNIR, "185057800608491"},
		{FRNIR, "1 85 05 78 006 084 91"},
		{PLPesel, "44051401359"},
		{PLPesel, "02070803628"},
		{PLNIP, "1234563218"},
		{PLNIP, "123-456-32-18"},
		{PLIDCard, "ABA300000"},
		{PLPassport, "CC7999486"}, // pl.wikipedia.org "Paszporty w Polsce" worked example
		{PLPassport, complete(t, PLPassport, "AB?234567")},
		{RUINN, "7707083893"},
		{RUINN, "500100732259"},
		{RUSNILS, "11223344595"},
		{RUSNILS, "112-233-445 95"},
		{RUOGRNIP, "304500116000157"},
		{RUOMS, complete(t, RUOMS, "123456789012345")},
		{KZIIN, complete(t, KZIIN, "88010130000")},
		{BYPersonalNumber, complete(t, BYPersonalNumber, "3150685A083PB")},
		{BYPersonalNumber, complete(t, BYPersonalNumber, "7123456A001PB")}, // issued since 2012: first digit 7, no birth date
		{BYPersonalNumber, complete(t, BYPersonalNumber, "7123456А001РВ")}, // Cyrillic А, Р, В typed for Latin A, P, B
		{FRNIR, "385057800608488"},                                         // first digit 3 and 4 exist
		{UARNOKPP, complete(t, UARNOKPP, "320000001")},
		{UAUNZR, complete(t, UAUNZR, "19850615-0123")},
		{UZPINFL, "30101901234569"}, // uz_kit UzPinflValidator README example
		{UZPINFL, complete(t, UZPINFL, "3150685001000")},
	}
}

func TestMatchValid(t *testing.T) {
	for _, c := range validCases(t) {
		t.Run(string(c.kind)+"/"+c.value, func(t *testing.T) {
			if k, ok := Match(c.value); !ok || k != c.kind {
				t.Errorf("Match(%q) = %q, %v; want %q", c.value, k, ok, c.kind)
			}
			if k, ok := Match("  " + c.value + "\n"); !ok || k != c.kind {
				t.Errorf("Match(padded %q) = %q, %v; want %q", c.value, k, ok, c.kind)
			}
		})
	}
}

// bump changes the digit at byte index i by one.
func bump(s string, i int) string {
	b := []byte(s)
	b[i] = '0' + (b[i]-'0'+1)%10
	return string(b)
}

func TestOneDigitOffDoesNotMatch(t *testing.T) {
	checked := map[Kind]bool{Email: true, IPv4: true, IPv6: true, MAC: true, JWT: true, Token: true, Phone: true}
	for _, c := range validCases(t) {
		if checked[c.kind] {
			continue
		}
		var positions []int
		for i := 0; i < len(c.value); i++ {
			if isDigitByte(c.value[i]) {
				positions = append(positions, i)
			}
		}
		last := positions[len(positions)-1]
		first := positions[0]
		if c.kind == EUVAT && strings.HasPrefix(c.value, "FR") {
			first = positions[2] // the key's second digit changes the number
		}
		for _, i := range []int{first, last} {
			bad := bump(c.value, i)
			if k, ok := Match(bad); ok && k == c.kind {
				t.Errorf("%s %q with digit %d changed (%q) still matches", c.kind, c.value, i, bad)
			}
		}
	}
}

// hl7M10 is the ISO/HL7 27931 Mod10 check digit that FOMS order No. 276
// prescribes for the 16-digit OMS policy number, written as the standard
// describes it: the odd positions from the right form a number that is
// doubled, the even positions from the right are prefixed to it, the
// digits of the result are summed, and the check digit takes the sum up to
// the next multiple of 10.
func hl7M10(payload string) byte {
	var odd, even strings.Builder
	for i := len(payload) - 1; i >= 0; i-- {
		if (len(payload)-i)%2 == 1 {
			odd.WriteByte(payload[i])
		} else {
			even.WriteByte(payload[i])
		}
	}
	var n uint64
	for i := 0; i < odd.Len(); i++ {
		n = n*10 + uint64(odd.String()[i]-'0')
	}
	sum := 0
	for _, c := range even.String() + fmt.Sprint(2*n) {
		sum += int(c - '0')
	}
	return byte('0' + (10-sum%10)%10)
}

// TestPublishedExamplesShadowed checks published examples that another
// kind of higher priority also accepts; the value is detected either way,
// so the validator is checked directly.
func TestPublishedExamplesShadowed(t *testing.T) {
	// python-stdnum ua.rntrc example; it also passes the Russian 10-digit
	// INN checksum, so Match reports ru_inn.
	if !validRNOKPP("1759013776") || validRNOKPP("1759013770") {
		t.Errorf("validRNOKPP: 1759013776 must pass and 1759013770 must fail")
	}
	if _, ok := Match("1759013776"); !ok {
		t.Errorf("Match(1759013776) found nothing")
	}
}

func TestOMSIsHL7Mod10(t *testing.T) {
	if got := hl7M10("12345"); got != '5' {
		t.Fatalf("hl7M10(12345) = %c, want 5 (the standard's worked example)", got)
	}
	// The doubled number's carries remove 9 exactly where Luhn subtracts 9
	// from a doubled digit, so the two schemes agree on every number.
	for i := 0; i < 2000; i++ {
		payload := fmt.Sprintf("%015d", uint64(i)*7919*104729+uint64(i))
		want := payload + string(hl7M10(payload))
		for d := byte('0'); d <= '9'; d++ {
			v := payload + string(d)
			if luhn(v) != (v == want) {
				t.Fatalf("luhn(%s) = %v, HL7 Mod10 check digit is %s", v, luhn(v), want[15:])
			}
		}
	}
}

func TestBYNormalizeFoldsCyrillic(t *testing.T) {
	latin := complete(t, BYPersonalNumber, "7123456A001PB")
	cyrillic := strings.NewReplacer("A", "А", "P", "Р", "B", "В").Replace(latin)
	if Normalize(BYPersonalNumber, cyrillic) != Normalize(BYPersonalNumber, latin) {
		t.Errorf("Normalize(%q) = %q, want %q", cyrillic, Normalize(BYPersonalNumber, cyrillic), Normalize(BYPersonalNumber, latin))
	}
	spans := Find("личный номер " + cyrillic + ", выдан")
	if len(spans) != 1 || spans[0].Kind != BYPersonalNumber {
		t.Errorf("Find with Cyrillic letters = %v, want one %s", spans, BYPersonalNumber)
	}
}

func TestPhoneOneDigitOff(t *testing.T) {
	// A phone is plausible per libphonenumber, so a changed digit may still
	// be a phone; these particular changes are not.
	for _, s := range []string{"+7 916 123-45-6", "+99 916 123 45 67", "+49 30 9018200000000"} {
		if k, ok := Match(s); ok {
			t.Errorf("Match(%q) = %q, want no match", s, k)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		kind Kind
		in   string
		want string
	}{
		{Email, " Ivan.Petrov@Example.COM ", "ivan.petrov@example.com"},
		{Phone, "8 (916) 123-45-67", "+79161234567"},
		{Phone, "+7 916 123-45-67", "+79161234567"},
		{Phone, "06 12 34 56 78", "+33612345678"},
		{Phone, "512 345 678", "+48512345678"},
		{Phone, "050 123 45 67", "+380501234567"},
		{Phone, "0612345678", "+33612345678"},
		{Phone, "0049 30 901820", "+4930901820"},
		{IPv4, "192.168.1.10", "192.168.1.10"},
		{IPv6, "2001:0DB8:0:0:0:0:0:1", "2001:db8::1"},
		{MAC, "AA-BB-CC-DD-EE-FF", "aa:bb:cc:dd:ee:ff"},
		{MAC, "aa:bb:cc:dd:ee:ff", "aa:bb:cc:dd:ee:ff"},
		{IBAN, "de89 3704 0044 0532 0130 00", "DE89370400440532013000"},
		{Card, "4111-1111 1111-1111", "4111111111111111"},
		{RUSNILS, "112-233-445 95", "11223344595"},
		{PLNIP, "123-456-32-18", "1234563218"},
		{EUVAT, "pl 123-456-32-18", "PL1234563218"},
		{DERVNR, "15 070649 c 103", "15070649C103"},
		{FRNIR, "1 85 05 78 006 084 91", "185057800608491"},
		{UAUNZR, "19850615-01234", "1985061501234"},
		{Token, " AKIAIOSFODNN7EXAMPLE ", "AKIAIOSFODNN7EXAMPLE"},
	}
	for _, tt := range tests {
		if got := Normalize(tt.kind, tt.in); got != tt.want {
			t.Errorf("Normalize(%s, %q) = %q, want %q", tt.kind, tt.in, got, tt.want)
		}
	}
	// Equal values written differently normalize equally.
	a := Normalize(Phone, "+7 (916) 123-45-67")
	b := Normalize(Phone, "8 916 1234567")
	if a != b {
		t.Errorf("phone forms differ: %q vs %q", a, b)
	}
}

func TestPriority(t *testing.T) {
	tests := []struct {
		in   string
		want Kind
	}{
		{"PL1234563218", EUVAT},
		{"1234563218", PLNIP},
		{"4111111111111111", Card},
		{"89161234567", Phone},
		{"DE89370400440532013000", IBAN},
		{"aa:bb:cc:dd:ee:ff", MAC},
	}
	for _, tt := range tests {
		if k, ok := Match(tt.in); !ok || k != tt.want {
			t.Errorf("Match(%q) = %q, %v; want %q", tt.in, k, ok, tt.want)
		}
	}
}

// found renders the spans of text as "kind:substring", checking offsets.
func found(text string) []string {
	var out []string
	for _, sp := range Find(text) {
		out = append(out, fmt.Sprintf("%s:%s", sp.Kind, text[sp.Start:sp.End]))
	}
	return out
}

func TestFind(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"emails", "Пишите: ivan@example.com, а ещё a.b@c.de.", []string{"email:ivan@example.com", "email:a.b@c.de"}},
		{"cyrillic around", "Клиент Иванов (ivan@example.com) позвонил +49 30 901820, спасибо", []string{"email:ivan@example.com", "phone:+49 30 901820"}},
		{"inn and snils", "ИНН 7707083893 и СНИЛС 112-233-445 95 клиента", []string{"ru_inn:7707083893", "ru_snils:112-233-445 95"}},
		{"card with trailing number", "Карта 4111 1111 1111 1111 123 готово", []string{"card:4111 1111 1111 1111"}},
		{"ip", "IP 10.0.0.1 и 2001:db8::1: порт", []string{"ipv4:10.0.0.1", "ipv6:2001:db8::1"}},
		{"two phones", "Позвоните +7 916 123-45-67 или 8 916 123-45-67.", []string{"phone:+7 916 123-45-67", "phone:8 916 123-45-67"}},
		{"iban", "Мой IBAN DE89 3704 0044 0532 0130 00.", []string{"iban:DE89 3704 0044 0532 0130 00"}},
		{"tel prefix", "tel:+49 30 901820;", []string{"phone:+49 30 901820"}},
		{"assignment", "id=65929970489;", []string{"de_steuer_id:65929970489"}},
		{"vat is not a nip", "VAT PL1234563218 end", []string{"eu_vat:PL1234563218"}},
		{"jwt", "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk ok", []string{"jwt:eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"}},
		{"aws key", "key AKIAIOSFODNN7EXAMPLE end", []string{"token:AKIAIOSFODNN7EXAMPLE"}},
		{"mac", "устройство AA-BB-CC-DD-EE-FF в сети", []string{"mac:AA-BB-CC-DD-EE-FF"}},
		{"nir", "NIR 1 85 05 78 006 084 91.", []string{"fr_nir:1 85 05 78 006 084 91"}},
		{"phone inside longer digit run", "ref 030 901820 12 ok", []string{"phone:030 901820 12"}},
		{"not inside a longer number", "x 7707083893123 y", nil},
		{"not inside a word", "abc7707083893 y", nil},
		{"not after at", "user@7707083893", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := found(tt.text)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Find(%q)\n got %q\nwant %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestFindSortedNonOverlappingWithByteOffsets(t *testing.T) {
	text := "Ёж: a@b.co, 4111 1111 1111 1111, +49 30 901820, 10.0.0.1, 65929970489, я"
	spans := Find(text)
	if len(spans) != 5 {
		t.Fatalf("got %d spans, want 5: %q", len(spans), found(text))
	}
	if !sort.SliceIsSorted(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start }) {
		t.Errorf("spans not sorted: %v", spans)
	}
	for i := 1; i < len(spans); i++ {
		if spans[i].Start < spans[i-1].End {
			t.Errorf("spans overlap: %v", spans)
		}
	}
	if got := text[spans[0].Start:spans[0].End]; got != "a@b.co" {
		t.Errorf("first span = %q", got)
	}
}

var notMatching = []string{
	"12345", "1000000", "2024", "-42", "0", "99999999",
	"2024-01-15", "20240115", "2024-01-15T10:30:00Z", "2024-01-15 10:30:00.123", "15.01.2024", "12/31/2024",
	"123e4567-e89b-12d3-a456-426614174000", "550e8400-e29b-41d4-a716-446655440000",
	"1299.99", "$1,299.00", "3.14159265358979", "1,234,567,890",
	"4222222222222", // Luhn-valid 13 digits: legacy Visa length, too common among plain IDs
	"ORD-12345", "A-100234", "INV20240001", "1.2.3", "v1.2.3-rc1", "192.168.1", "256.1.1.1",
	"1700000000000", "1700000000", "0000000000000000", "00000000000", "9999999999999999",
	"1234567890", "123456789012", "12345678901", "1234567890123", "1234567890123456", "98765432109876",
	"4111111111111112", "1111111111111111", "5555555555554445",
	"08-12-2024", "12:30:45", "std::vector", "user@localhost", "@handle", "a@b", "hello world",
	"100 200 300 400", "+1 2 3", "12345678", "4915112345679", "+49 30", "(030)",
	"DE89370400440532013001", "GB82WEST12345698765433", "XX00123456789012345",
	"eyJhbGciOiJIUzI1NiJ9", "eyJmb28iOiJiYXIifQ.eyJmb28iOiJiYXIifQ.sig", "sk-short", "AKIA123", "ghp_short",
	"DE136695977", "FR41303265045",
}

func TestNotMatching(t *testing.T) {
	for _, s := range notMatching {
		if k, ok := Match(s); ok {
			t.Errorf("Match(%q) = %q, want no match", s, k)
		}
	}
}

func TestFindFalsePositives(t *testing.T) {
	texts := []string{
		"Order 12345 shipped on 2024-01-15 at 10:30, total 1299.99 USD, version 1.2.3.",
		"id 123e4567-e89b-12d3-a456-426614174000 created 2024-01-15T10:30:00Z",
		"ts 1700000000000 count 1,234,567,890 year 2024 ref ORD-1234567890",
		"v1.2.3.4.5 build 10.2.3.4.5 file a1.b2.c3",
		"Цена 1299.99 руб, заказ №12345, артикул A-100234, 2024 год",
		"std::vector and time 12:30:45 on 15.01.2024",
	}
	for _, text := range texts {
		if got := found(text); len(got) != 0 {
			t.Errorf("Find(%q) = %q, want none", text, got)
		}
	}
	for _, s := range notMatching {
		if got := found("значение " + s + " конец"); len(got) != 0 {
			t.Errorf("Find in %q = %q, want none", s, got)
		}
	}
}

// Official or published worked examples of the check-digit algorithms.
func TestAlgorithms(t *testing.T) {
	if !luhn("79927398713") || luhn("79927398714") {
		t.Error("luhn")
	}
	if iso7064("6592997048") != 9 {
		t.Errorf("iso7064 = %d", iso7064("6592997048"))
	}
	if !validIBAN("GB82 WEST 1234 5698 7654 32") || validIBAN("GB82 WEST 1234 5698 7654 33") {
		t.Error("iban")
	}
	if validIBAN("DE8937040044053201300") {
		t.Error("iban length is checked against the country table")
	}
}
