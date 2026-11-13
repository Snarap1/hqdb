package mask

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Snarap1/hqdb/internal/values"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

var reMarkerExact = regexp.MustCompile(`^masked-[0-9a-f]{12}$`)

func newMasker(t *testing.T, masking map[string]any) *Masker {
	t.Helper()
	m, err := New(masking, testKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func isMarker(v any) bool {
	s, ok := v.(string)
	return ok && reMarkerExact.MatchString(s)
}

func TestNewKey(t *testing.T) {
	a, b := NewKey(), NewKey()
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("key lengths = %d, %d, want 32", len(a), len(b))
	}
	if string(a) == string(b) {
		t.Error("two keys are equal")
	}
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name    string
		masking map[string]any
		want    string
	}{
		{"nil map", nil, ""},
		{"empty map", map[string]any{}, ""},
		{"valid", map[string]any{"columns": []any{"name"}, "allow": []any{"*_verified"}}, ""},
		{"unknown key", map[string]any{"strategy": "hash"},
			`unknown option "strategy" in [masking]; valid options: columns, allow`},
		{"columns not an array", map[string]any{"columns": "name"},
			`[masking] columns must be an array of strings`},
		{"allow not an array", map[string]any{"allow": true},
			`[masking] allow must be an array of strings`},
		{"columns entry not a string", map[string]any{"columns": []any{"name", 3}},
			`[masking] columns[1] must be a string`},
		{"allow entry not a string", map[string]any{"allow": []any{nil}},
			`[masking] allow[0] must be a string`},
		{"malformed column glob", map[string]any{"columns": []any{"na[me"}},
			`[masking] columns: malformed glob "na[me"`},
		{"malformed allow glob", map[string]any{"allow": []any{"ok", "x\\"}},
			`[masking] allow: malformed glob "x\\"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := New(tc.masking, testKey)
			if tc.want == "" {
				if err != nil || m == nil {
					t.Fatalf("New = %v, %v; want success", m, err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("New error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"firstName":      "first_name",
		"First-Name":     "first_name",
		"first name":     "first_name",
		"  FIRST__NAME ": "first_name",
		"userID":         "user_id",
		"XMLHttpRequest": "xml_http_request",
		"prénom":         "prenom",
		"straße":         "strasse",
		"nazwisko_ł":     "nazwisko_l",
		"Łódź":           "lodz",
		"Имя":            "имя",
		"ФамилияИмя":     "фамилия_имя",
		"ім'я":           "ім_я",
		"ipv4":           "ipv4",
		"a.b.c":          "a_b_c",
		"":               "",
		"___":            "",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNameRules(t *testing.T) {
	cases := []struct {
		column string
		want   string // "", "hash" or "redact"
	}{
		{"first_name", "hash"},
		{"firstName", "hash"},
		{"user_first_name", "hash"},
		{"First-Name", "hash"},
		{"firstname", "hash"},
		{"first_x", ""},
		{"name", ""},
		{"product_name", ""},
		{"table_name", ""},
		{"prénom", "hash"},
		{"Straße", "hash"},
		{"имя", "hash"},
		{"Фамилия", "hash"},
		{"прізвище", "hash"},
		{"nazwisko", "hash"},
		{"f", "hash"},
		{"i", "hash"},
		{"o", "hash"},
		{"f_x", ""},
		{"tel", "hash"},
		{"tel_x", ""},
		{"ort", "hash"},
		{"ort_id", ""},
		{"lat", "hash"},
		{"date_of_birth", "hash"},
		{"dob", "hash"},
		{"customer_email", "hash"},
		{"customerEmail", "hash"},
		{"phone", "hash"},
		{"telefon", "hash"},
		{"ip_address", "hash"},
		{"password", "hash"},
		{"api_key", "hash"},
		{"iban", "hash"},
		{"pesel", "hash"},
		{"инн", "hash"},
		{"diagnosis", "redact"},
		{"patient_diagnosis", "redact"},
		{"religion", "redact"},
		{"Blutgruppe", "redact"},
		{"blood_type", "redact"},
		{"trade_union", "redact"},
		{"id", ""},
		{"price", ""},
		{"created_at", ""},
	}
	m := newMasker(t, nil)
	for _, tc := range cases {
		t.Run(tc.column, func(t *testing.T) {
			got, changed := m.Value(tc.column, "some value")
			switch tc.want {
			case "":
				if changed || got != "some value" {
					t.Errorf("Value = %v, %v; want unchanged", got, changed)
				}
			case "hash":
				if !changed || !isMarker(got) {
					t.Errorf("Value = %v, %v; want marker", got, changed)
				}
			case "redact":
				if !changed || got != "masked-redacted" {
					t.Errorf("Value = %v, %v; want masked-redacted", got, changed)
				}
			}
		})
	}
}

func TestUserColumnsAndAllow(t *testing.T) {
	m := newMasker(t, map[string]any{
		"columns": []any{"name", "nick*"},
		"allow":   []any{"product_name", "*_verified", "first_name"},
	})
	cases := []struct {
		column  string
		changed bool
	}{
		{"name", true},
		{"Name", true},
		{"nickname", true},
		{"nick", true},
		{"product_name", false},
		{"email_verified", false},
		{"first_name", false}, // allow wins over built-ins
		{"last_name", true},
		{"price", false},
	}
	for _, tc := range cases {
		_, changed := m.Value(tc.column, "some value")
		if changed != tc.changed {
			t.Errorf("Value(%q) changed = %v, want %v", tc.column, changed, tc.changed)
		}
	}
	// allow wins over special categories too.
	m2 := newMasker(t, map[string]any{"allow": []any{"diagnosis"}})
	if _, changed := m2.Value("diagnosis", "flu"); changed {
		t.Error("allowed special category column was masked")
	}
}

func TestPassthrough(t *testing.T) {
	m := newMasker(t, map[string]any{"columns": []any{"name"}})
	for _, col := range []string{"first_name", "diagnosis", "name", "price"} {
		for _, v := range []any{nil, "", "   ", "\t\n"} {
			got, changed := m.Value(col, v)
			if changed || !reflect.DeepEqual(got, v) {
				t.Errorf("Value(%q, %q) = %v, %v; want unchanged", col, v, got, changed)
			}
		}
	}
}

func TestMarkerDeterminismAndLinking(t *testing.T) {
	m := newMasker(t, nil)
	a, _ := m.Value("first_name", "Ivan")
	b, _ := m.Value("last_name", "Ivan")
	c, _ := m.Value("first_name", "  IVAN ")
	d, _ := m.Value("first_name", "Petr")
	if !isMarker(a) {
		t.Fatalf("marker = %v", a)
	}
	if a != b || a != c {
		t.Errorf("equal values got different markers: %v %v %v", a, b, c)
	}
	if a == d {
		t.Errorf("different values share a marker: %v", a)
	}
	// Same key in a fresh Masker: same marker; another key: different.
	m2 := newMasker(t, nil)
	if a2, _ := m2.Value("first_name", "Ivan"); a2 != a {
		t.Errorf("fresh masker under the same key: %v != %v", a2, a)
	}
	m3, _ := New(nil, []byte("another key"))
	if a3, _ := m3.Value("first_name", "Ivan"); a3 == a {
		t.Error("different keys gave the same marker")
	}
	// Unicode normalization and case folding.
	e, _ := m.Value("first_name", "Straße")
	f, _ := m.Value("first_name", "STRASSE")
	if e != f {
		t.Errorf("case-folded values differ: %v %v", e, f)
	}
	g, _ := m.Value("first_name", "é")
	h, _ := m.Value("first_name", "é")
	if g != h {
		t.Errorf("NFC-equal values differ: %v %v", g, h)
	}
}

func TestSpecialCategoryHasNoHash(t *testing.T) {
	m := newMasker(t, nil)
	for _, v := range []any{"flu", "cold", 42, true, 1.5, time.Now()} {
		got, changed := m.Value("diagnosis", v)
		if !changed || got != "masked-redacted" {
			t.Errorf("Value(diagnosis, %v) = %v, %v", v, got, changed)
		}
	}
}

func TestNonStringValuesUnderNameRule(t *testing.T) {
	m := newMasker(t, nil)
	ts := time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC)
	bday, changed := m.Value("birthday", ts)
	if !changed || !isMarker(bday) {
		t.Fatalf("time under name rule = %v, %v", bday, changed)
	}
	same, _ := m.Value("dob", "1990-05-17T00:00:00Z")
	if same != bday {
		t.Errorf("time.Time and its RFC 3339 string differ: %v %v", bday, same)
	}
	n1, _ := m.Value("passport", 12345)
	n2, _ := m.Value("passport", int64(12345))
	n3, _ := m.Value("passport", "12345")
	n4, _ := m.Value("passport", float64(12345))
	if !isMarker(n1) || n1 != n2 || n1 != n3 || n1 != n4 {
		t.Errorf("integer forms differ: %v %v %v %v", n1, n2, n3, n4)
	}
	if b, ch := m.Value("email", true); !ch || !isMarker(b) {
		t.Errorf("bool under name rule = %v, %v", b, ch)
	}
	// Non-name integers and bools stay.
	if v, ch := m.Value("count", 5); ch || v != 5 {
		t.Errorf("plain int = %v, %v", v, ch)
	}
	if v, ch := m.Value("flag", true); ch || v != true {
		t.Errorf("plain bool = %v, %v", v, ch)
	}
}

func TestNestedUnderNameRuleMaskedWhole(t *testing.T) {
	m := newMasker(t, nil)
	got, changed := m.Value("address", map[string]any{"city": "Berlin", "zip": "10115"})
	if !changed || !isMarker(got) {
		t.Fatalf("nested object under name rule = %v, %v", got, changed)
	}
	got, changed = m.Value("phone", []any{"a", "b"})
	if !changed || !isMarker(got) {
		t.Fatalf("array under name rule = %v, %v", got, changed)
	}
	got, changed = m.Value("health", values.Doc{{Key: "x", Value: "y"}})
	if !changed || got != "masked-redacted" {
		t.Fatalf("object under special rule = %v, %v", got, changed)
	}
}

func TestRowsAndRow(t *testing.T) {
	m := newMasker(t, nil)
	cols := []string{"id", "first_name", "price", "last_name", "diagnosis"}
	rows := [][]any{
		{1, "Ivan", 9.5, "Ivan", "flu"},
		{2, nil, 3.0, "", "cold"},
	}
	orig := []any{1, "Ivan", 9.5, "Ivan", "flu"}
	if got := m.Row(cols, orig); !isMarker(got[1]) || got[1] != got[3] || got[4] != "masked-redacted" || got[0] != 1 {
		t.Errorf("Row = %v", got)
	}
	if orig[1] != "Ivan" {
		t.Error("Row modified its input")
	}
	masked := m.Rows(cols, rows)
	if want := []string{"first_name", "last_name", "diagnosis"}; !reflect.DeepEqual(masked, want) {
		t.Errorf("masked columns = %v, want %v", masked, want)
	}
	if !isMarker(rows[0][1]) || rows[1][1] != nil || rows[1][3] != "" || rows[1][2] != 3.0 {
		t.Errorf("rows = %v", rows)
	}
	if got := m.Rows([]string{"id"}, [][]any{{1}}); len(got) != 0 {
		t.Errorf("masked columns for clean rows = %v", got)
	}
}

func TestJSONInString(t *testing.T) {
	m := newMasker(t, nil)
	got, changed := m.Value("payload", `{"id":1,"first_name":"Ivan","tags":["a"],"diagnosis":"flu"}`)
	if !changed {
		t.Fatal("JSON string not masked")
	}
	s := got.(string)
	var doc map[string]any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("result is not JSON: %v (%s)", err, s)
	}
	if !isMarker(doc["first_name"]) || doc["diagnosis"] != "masked-redacted" || doc["id"] != float64(1) {
		t.Errorf("doc = %v", doc)
	}
	if !strings.HasPrefix(s, `{"id":1,"first_name":"masked-`) {
		t.Errorf("not compact or order lost: %s", s)
	}
	// JSON with nothing to mask comes back byte-identical.
	plain := `{ "id": 1,  "x": "y" }`
	got, changed = m.Value("payload", plain)
	if changed || got != plain {
		t.Errorf("clean JSON = %v, %v", got, changed)
	}
	// Broken JSON is plain text.
	got, changed = m.Value("payload", `{"first_name": `)
	if changed || got != `{"first_name": ` {
		t.Errorf("broken JSON = %v, %v", got, changed)
	}
}

func TestDoc(t *testing.T) {
	m := newMasker(t, map[string]any{"columns": []any{"contacts.nick", "contacts.phones.number"}})
	in := values.Doc{
		{Key: "_id", Value: "1"},
		{Key: "firstName", Value: "Ivan"},
		{Key: "contacts", Value: values.Doc{
			{Key: "phones", Value: []any{
				values.Doc{{Key: "number", Value: "123"}, {Key: "kind", Value: "home"}},
				values.Doc{{Key: "number", Value: "456"}},
			}},
			{Key: "nick", Value: "vanya"},
		}},
		{Key: "address", Value: values.Doc{{Key: "city", Value: "Berlin"}}},
		{Key: "notes", Value: []any{"a", "b"}},
		{Key: "diagnosis", Value: "flu"},
		{Key: "plain", Value: nil},
	}
	got, paths := m.Doc(in)
	wantPaths := []string{"address", "contacts.nick", "contacts.phones.number", "diagnosis", "firstName"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("paths = %v, want %v", paths, wantPaths)
	}
	d, ok := got.(values.Doc)
	if !ok {
		t.Fatalf("result type = %T", got)
	}
	keys := make([]string, len(d))
	for i, f := range d {
		keys[i] = f.Key
	}
	if !reflect.DeepEqual(keys, []string{"_id", "firstName", "contacts", "address", "notes", "diagnosis", "plain"}) {
		t.Errorf("field order = %v", keys)
	}
	if v, _ := d.Get("_id"); v != "1" {
		t.Errorf("_id = %v", v)
	}
	if v, _ := d.Get("firstName"); !isMarker(v) {
		t.Errorf("firstName = %v", v)
	}
	if v, _ := d.Get("address"); !isMarker(v) {
		t.Errorf("address = %v", v)
	}
	if v, _ := d.Get("diagnosis"); v != "masked-redacted" {
		t.Errorf("diagnosis = %v", v)
	}
	if v, _ := d.Get("notes"); !reflect.DeepEqual(v, []any{"a", "b"}) {
		t.Errorf("notes = %v", v)
	}
	c, _ := d.Get("contacts")
	cd := c.(values.Doc)
	if v, _ := cd.Get("nick"); !isMarker(v) {
		t.Errorf("contacts.nick = %v", v)
	}
	ph, _ := cd.Get("phones")
	first := ph.([]any)[0].(values.Doc)
	if v, _ := first.Get("number"); !isMarker(v) {
		t.Errorf("phones[0].number = %v", v)
	}
	if v, _ := first.Get("kind"); v != "home" {
		t.Errorf("phones[0].kind = %v", v)
	}
	// The input is not modified.
	if v, _ := in.Get("firstName"); v != "Ivan" {
		t.Error("Doc modified its input")
	}
}

func TestDocMapAndArrays(t *testing.T) {
	m := newMasker(t, nil)
	in := map[string]any{
		"users": []any{
			map[string]any{"surname": "Petrov", "age": 3},
			map[string]any{"surname": "Petrov"},
		},
	}
	got, paths := m.Doc(in)
	if !reflect.DeepEqual(paths, []string{"users.surname"}) {
		t.Errorf("paths = %v", paths)
	}
	users := got.(map[string]any)["users"].([]any)
	a := users[0].(map[string]any)
	b := users[1].(map[string]any)
	if !isMarker(a["surname"]) || a["surname"] != b["surname"] || a["age"] != 3 {
		t.Errorf("users = %v", users)
	}
	if got, paths := m.Doc("plain"); got != "plain" || len(paths) != 0 {
		t.Errorf("scalar Doc = %v, %v", got, paths)
	}
	if got, paths := m.Doc(nil); got != nil || len(paths) != 0 {
		t.Errorf("nil Doc = %v, %v", got, paths)
	}
}

func TestDocArrayUnderNameRuleMaskedWhole(t *testing.T) {
	m := newMasker(t, nil)
	got, paths := m.Doc(values.Doc{{Key: "emails", Value: []any{"x", "y"}}, {Key: "phone", Value: []any{"1"}}})
	d := got.(values.Doc)
	if v, _ := d.Get("phone"); !isMarker(v) {
		t.Errorf("phone = %v", v)
	}
	if !reflect.DeepEqual(paths, []string{"phone"}) {
		t.Errorf("paths = %v", paths)
	}
}

func TestMarkerViolation(t *testing.T) {
	const tail = "; masked values are placeholders, not data"
	cases := []struct {
		name, text, want string
	}{
		{"clean", "SELECT 1", ""},
		{"empty", "", ""},
		{"marker", "SELECT * FROM t WHERE a = 'masked-3f9a1c2b4d5e'",
			"statement contains a masked value (masked-3f9a1c2b4d5e)" + tail},
		{"redacted", `{"x":"masked-redacted"}`,
			"statement contains a masked value (masked-redacted)" + tail},
		{"first of two", "masked-aaaaaaaaaaaa masked-bbbbbbbbbbbb",
			"statement contains a masked value (masked-aaaaaaaaaaaa)" + tail},
		{"too long hex", "masked-3f9a1c2b4d5e0", ""},
		{"too short hex", "masked-3f9a1c2b4d5", ""},
		{"uppercase hex", "masked-3F9A1C2B4D5E",
			"statement contains a masked value (masked-3F9A1C2B4D5E)" + tail},
		{"all upper case", "WHERE a = 'MASKED-3F9A1C2B4D5E'",
			"statement contains a masked value (MASKED-3F9A1C2B4D5E)" + tail},
		{"mixed case redacted", "x = 'Masked-Redacted'",
			"statement contains a masked value (Masked-Redacted)" + tail},
		{"upper case redacted", "MASKED-REDACTED",
			"statement contains a masked value (MASKED-REDACTED)" + tail},
		{"kelvin sign", "maſKed-3f9a1c2b4d5e",
			"statement contains a masked value (maſKed-3f9a1c2b4d5e)" + tail},
		{"prefixed word", "xmasked-3f9a1c2b4d5e", ""},
		{"prefixed word upper case", "XMASKED-3F9A1C2B4D5E", ""},
		{"redacted continues", "masked-redactedx", ""},
		{"upper case too long hex", "MASKED-3F9A1C2B4D5E0", ""},
		{"upper case not hex", "MASKED-3F9A1C2B4D5G", ""},
		{"word masked alone", "SELECT Masked, MASKED_x FROM t", ""},
		{"in parens", "f(masked-0123456789ab)",
			"statement contains a masked value (masked-0123456789ab)" + tail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MarkerViolation(tc.text); got != tc.want {
				t.Errorf("MarkerViolation(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
	// A marker the masker produced is always caught.
	m := newMasker(t, nil)
	mk, _ := m.Value("first_name", "Ivan")
	if MarkerViolation("WHERE a = '"+mk.(string)+"'") == "" {
		t.Error("produced marker not caught")
	}
}

// The tests below depend on internal/mask/detect recognizing an email.
// They fail only while detect is still a stub.

func TestDetectDependentEmail(t *testing.T) {
	m := newMasker(t, nil)
	const email = "ivan@example.com"
	byValue, changed := m.Value("contact", email)
	if !changed || !isMarker(byValue) {
		t.Fatalf("[needs detect] email in a plain column = %v, %v", byValue, changed)
	}
	byName, _ := m.Value("email", "IVAN@Example.com")
	if byName != byValue {
		t.Errorf("[needs detect] name-rule and value-rule markers differ: %v %v", byName, byValue)
	}
	// An allowed column still runs the detectors.
	m2 := newMasker(t, map[string]any{"allow": []any{"email"}})
	if v, changed := m2.Value("email", email); !changed || !isMarker(v) {
		t.Errorf("[needs detect] allowed column with an email = %v, %v", v, changed)
	}
	if v, changed := m2.Value("email", "not personal"); changed {
		t.Errorf("allowed column with plain text = %v", v)
	}
	// Free text: spans replaced.
	got, changed := m.Value("comment", "write to "+email+" today")
	if !changed || strings.Contains(got.(string), "@") || !strings.HasPrefix(got.(string), "write to masked-") || !strings.HasSuffix(got.(string), " today") {
		t.Errorf("[needs detect] free text = %v, %v", got, changed)
	}
	if text := m.Text("a " + email + " b " + email); strings.Count(text, byValue.(string)) != 2 || strings.Contains(text, "@") {
		t.Errorf("[needs detect] Text = %q", text)
	}
	// JSON-in-string with an email under a plain key.
	got, changed = m.Value("payload", `{"contact":"`+email+`"}`)
	if !changed || strings.Contains(got.(string), "@") {
		t.Errorf("[needs detect] JSON with email = %v, %v", got, changed)
	}
	// Doc with a detected value under a plain key.
	doc, paths := m.Doc(values.Doc{{Key: "contact", Value: email}})
	if v, _ := doc.(values.Doc).Get("contact"); !isMarker(v) || !reflect.DeepEqual(paths, []string{"contact"}) {
		t.Errorf("[needs detect] Doc = %v, %v", doc, paths)
	}
}

func TestTextWithoutDetections(t *testing.T) {
	m := newMasker(t, nil)
	if got := m.Text("plain text, no personal data"); got != "plain text, no personal data" {
		t.Errorf("Text = %q", got)
	}
	if got := m.Text(""); got != "" {
		t.Errorf("Text(\"\") = %q", got)
	}
}
