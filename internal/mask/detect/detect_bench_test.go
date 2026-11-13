package detect

import (
	"strings"
	"testing"
)

const benchClean1K = "Order shipped to the warehouse on Monday. The customer asked to change the delivery window " +
	"and to leave the parcel at the reception. Item count 3, total 129.90 EUR, status confirmed. " +
	"Notes: fragile, keep upright, do not stack. Reference SKU-000123 and SKU-000456, batch 2024-03. " +
	"Version v1.2.3 of the label template was used; the courier will call ahead before arriving. " +
	"Previous attempts: none. Payment method on file, invoice to follow by post next week. " +
	"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore " +
	"et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip " +
	"ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore."

const benchDirty1K = "Contact ivan.petrov@example.com or call +7 916 123-45-67, backup 8 (916) 123-45-67. " +
	"Card 4111 1111 1111 1111 exp 12/27, IBAN DE89 3704 0044 0532 0130 00. Login from 192.168.1.10 and " +
	"fe80::1ff:fe23:4567:890a, MAC aa:bb:cc:dd:ee:ff. INN 7707083893, SNILS 112-233-445 95, PESEL 44051401359. " +
	"Second contact anna.k@mail.ru, +49 30 901820. Key AKIAIOSFODNN7EXAMPLE leaked in ticket 55123. " +
	"Order 1234567 shipped on 2024-03-15, total 129.90 EUR, phone 06 12 34 56 78 for France. " +
	"VAT DE136695976, NIP 123-456-32-18. Ref 3f2504e0-4f89-11d3-9a0c-0305e82c3301. More text follows here."

func BenchmarkMatch(b *testing.B) {
	cases := []struct{ name, value string }{
		{"clean_word", "active"},
		{"clean_uuid", "3f2504e0-4f89-11d3-9a0c-0305e82c3301"},
		{"clean_sent", "The quick brown fox jumps over the lazy dog"},
		{"clean_digits", "1234567"},
		{"email", "ivan.petrov+tag@example.co.uk"},
		{"phone", "+7 916 123-45-67"},
		{"card", "4111111111111111"},
		{"inn10", "7707083893"},
		{"last_rnokpp", "3200000019"},
		{"clean_1k", benchClean1K},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Match(c.value)
			}
		})
	}
}

func BenchmarkFind(b *testing.B) {
	cases := []struct{ name, text string }{
		{"clean_short", "Blue cotton T-shirt, size M"},
		{"clean_1k", benchClean1K},
		{"dirty_1k", benchDirty1K},
		{"clean_64k", strings.Repeat(benchClean1K, 64)},
		{"dirty_64k", strings.Repeat(benchDirty1K, 64)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(c.text)))
			for i := 0; i < b.N; i++ {
				Find(c.text)
			}
		})
	}
}

// Pathological inputs: long digit runs, long separator runs, many '@'.
func BenchmarkFindPathological(b *testing.B) {
	cases := []struct{ name, text string }{
		{"digits_10k", strings.Repeat("1", 10000)},
		{"digit_groups_10k", strings.Repeat("12 ", 3400)},
		{"dashes_digits", strings.Repeat("1-2-", 2500)},
		{"parens_digits", "8 " + strings.Repeat("( ) ", 2500) + "916 123-45-67"},
		{"phone_list_100", strings.Repeat("8 916 123-45-67 ", 100)},
		{"at_signs", strings.Repeat("a@", 5000)},
		{"colons", strings.Repeat("a:", 5000)},
		{"dots_digits", strings.Repeat("1.", 5000)},
		{"email_local_long", strings.Repeat("a", 10000) + "@"},
		{"hex_colon_runs", strings.Repeat("ab:", 3400)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(c.text)))
			for i := 0; i < b.N; i++ {
				Find(c.text)
			}
		})
	}
}
