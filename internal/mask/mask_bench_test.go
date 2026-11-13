package mask

import (
	"fmt"
	"strings"
	"testing"
)

var benchKey = []byte("0123456789abcdef0123456789abcdef")

const benchCleanText = "Order shipped to the warehouse on Monday. The customer asked to change the delivery window " +
	"and to leave the parcel at the reception. Item count 3, total 129.90 EUR, status confirmed."

const benchDirtyText = "Contact ivan.petrov@example.com or call +7 916 123-45-67, card 4111 1111 1111 1111, " +
	"IBAN DE89 3704 0044 0532 0130 00, from 192.168.1.10, INN 7707083893, second anna.k@mail.ru +49 30 901820."

type rowCase struct {
	name string
	cols []string
	row  func(int) []any
}

// rowCases are typical result shapes: narrow and wide tables, with and
// without personal data, free text, NULLs, numbers, JSON, and a large blob.
var rowCases = []rowCase{
	{"narrow", []string{"id", "status", "amount", "created_at", "sku"}, func(i int) []any {
		return []any{int64(100000 + i), "active", 129.9, "2024-03-15T10:22:01Z", fmt.Sprintf("SKU-%06d", i)}
	}},
	{"wide", []string{
		"id", "user_id", "first_name", "last_name", "email", "phone", "birth_date", "status", "plan",
		"amount", "currency", "created_at", "updated_at", "deleted_at", "product_name", "description",
		"tags", "metadata", "ip_address", "country", "score", "is_active", "external_ref", "notes",
	}, func(i int) []any {
		return []any{
			int64(i), int64(500000 + i), "Ivan", "Petrov", fmt.Sprintf("ivan%d@example.com", i), "+7 916 123-45-67",
			"1985-06-15", "active", "pro", 129.9, "EUR", "2024-03-15T10:22:01Z", "2024-03-16T08:00:00Z", nil,
			"Blue cotton T-shirt", benchCleanText, `["new","vip","eu"]`,
			fmt.Sprintf(`{"source":"web","contact":{"email":"a%d@b.io"},"n":3}`, i), "192.168.1.10", "DE", 0.75, true,
			"3f2504e0-4f89-11d3-9a0c-0305e82c3301", benchDirtyText,
		}
	}},
	{"wide_clean", []string{
		"id", "order_id", "status", "plan", "amount", "currency", "created_at", "updated_at", "deleted_at",
		"product_name", "description", "tags", "metadata", "country", "score", "is_active", "external_ref",
		"warehouse", "carrier", "tracking_state", "weight_kg", "items", "channel", "comment_text",
	}, func(i int) []any {
		return []any{
			int64(i), int64(500000 + i), "active", "pro", 129.9, "EUR", "2024-03-15T10:22:01Z",
			"2024-03-16T08:00:00Z", nil, "Blue cotton T-shirt", benchCleanText, `["new","vip","eu"]`,
			`{"source":"web","campaign":"spring","n":3}`, "DE", 0.75, true, "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
			"Berlin-2", "DHL", "in_transit", 1.25, int64(3), "web", "Please deliver after 5pm, thanks.",
		}
	}},
	{"dirty_text", []string{"comment", "body", "payload"}, func(int) []any {
		return []any{benchDirtyText, benchDirtyText + " " + benchCleanText, benchCleanText + " " + benchDirtyText}
	}},
	{"nulls", []string{"a", "b", "c", "d", "e", "f", "g", "h"}, func(int) []any {
		return []any{nil, nil, nil, nil, nil, nil, nil, nil}
	}},
	{"numerics", []string{"id", "qty", "price", "ratio", "big", "epoch_ms", "count", "flag"}, func(i int) []any {
		return []any{int64(i), int64(3), 129.9, 0.25, int64(9007199254740), int64(1710497521000), int64(42), true}
	}},
	{"json", []string{"id", "doc"}, func(i int) []any {
		return []any{int64(i), fmt.Sprintf(`{"user":{"first_name":"Ivan","email":"i%d@x.ru","phone":"+7 916 123-45-67"},`+
			`"items":[{"sku":"A1","qty":2},{"sku":"B2","qty":1}],"note":"%s","ts":"2024-03-15T10:22:01Z"}`, i, benchCleanText)}
	}},
	{"blob64k", []string{"id", "body"}, func(i int) []any {
		return []any{int64(i), strings.Repeat(benchCleanText+" ", 64*1024/(len(benchCleanText)+1))}
	}},
}

func BenchmarkRow(b *testing.B) {
	m, _ := New(nil, benchKey)
	for _, rc := range rowCases {
		rows := make([][]any, 64)
		for i := range rows {
			rows[i] = rc.row(i)
		}
		b.Run(rc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.Row(rc.cols, rows[i%len(rows)])
			}
		})
	}
}

func BenchmarkClassify(b *testing.B) {
	m, _ := New(map[string]any{"columns": []any{"client_*"}, "allow": []any{"product_name"}}, benchKey)
	cases := []struct {
		name  string
		paths [][]string
	}{
		{"wide_24_cols", func() [][]string {
			var out [][]string
			for _, c := range rowCases[1].cols {
				out = append(out, []string{c})
			}
			return out
		}()},
		{"one_col_clean", [][]string{{"tracking_state"}}},
		{"doc_path_3", [][]string{{"user", "contact", "zipCode"}}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, p := range c.paths {
					m.classify(p)
				}
			}
		})
	}
}

func BenchmarkNormalizeName(b *testing.B) {
	for _, n := range []string{"id", "created_at", "firstName", "Customer Email Address", "фамилия_клиента", "Straße_Nr"} {
		b.Run(n, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				normalizeName(n)
			}
		})
	}
}

func BenchmarkRuleMatch(b *testing.B) {
	for _, n := range []string{"id", "tracking_state", "customer_billing_address_line_2", "first_name"} {
		b.Run(n, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				specialSet.matches(n)
				builtinRules.matches(n)
			}
		})
	}
}

func BenchmarkMarker(b *testing.B) {
	m, _ := New(nil, benchKey)
	long := strings.Repeat("x", 1024)
	cases := []struct {
		name string
		f    func()
	}{
		{"hmac_short", func() { m.marker("ivan.petrov@example.com") }},
		{"hmac_1k", func() { m.marker(long) }},
		{"foldText_short", func() { foldText("  Ivan Petrov ") }},
		{"hashText_name_ivan", func() { m.hashText("Ivan") }},
		{"hashText_phone", func() { m.hashText("+7 916 123-45-67") }},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.f()
			}
		})
	}
}

func BenchmarkMarkerViolation(b *testing.B) {
	base := "SELECT id, email, phone FROM customers c JOIN orders o ON o.customer_id = c.id WHERE o.total > 100 AND c.status = 'active' "
	for _, size := range []int{128, 64 * 1024} {
		s := strings.Repeat(base, size/len(base)+1)[:size]
		for _, withMarker := range []bool{false, true} {
			text := s
			if withMarker {
				text = s + " AND email = 'masked-3f9a1c2b4d5e'"
			}
			b.Run(fmt.Sprintf("%d/marker=%v", size, withMarker), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(text)))
				for i := 0; i < b.N; i++ {
					MarkerViolation(text)
				}
			})
		}
	}
	near := strings.Repeat("SELECT masked, masked_x, masked-1 FROM t; ", 1000)
	b.Run("near_miss_40k", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(near)))
		for i := 0; i < b.N; i++ {
			MarkerViolation(near)
		}
	})
}

func BenchmarkErrorText(b *testing.B) {
	m, _ := New(nil, benchKey)
	cases := []struct{ name, text string }{
		{"pg_unique", `ERROR: duplicate key value violates unique constraint "users_email_key" (SQLSTATE 23505): Key (email)=(ivan@x.ru) already exists.`},
		{"pg_syntax", `ERROR: syntax error at or near "FORM" (SQLSTATE 42601)`},
		{"conn_error", `failed to connect to host=10.0.12.5 user=app database=prod: dial error (dial tcp 10.0.12.5:5432: connect: connection refused)`},
		{"long_4k", strings.Repeat(`ERROR: column "foo" does not exist at character 123; `, 80)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.Text(c.text)
			}
		})
	}
}
