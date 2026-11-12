package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/mask"
	"github.com/Snarap1/hqdb/internal/readonly"
)

func testMasker(t *testing.T) *mask.Masker {
	t.Helper()
	m, err := mask.New(nil, []byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func markerOf(t *testing.T, m *mask.Masker, column string, v any) string {
	t.Helper()
	out, changed := m.Value(column, v)
	s, _ := out.(string)
	if !changed || !strings.HasPrefix(s, "masked-") {
		t.Fatalf("Value(%q, %v) = %v, changed=%v; want a marker", column, v, out, changed)
	}
	return s
}

func TestGuard(t *testing.T) {
	m := testMasker(t)
	marker := markerOf(t, m, "email", "ivan@example.com")
	markerReason := mask.MarkerViolation(marker)
	write := "DELETE FROM t"
	writeWithMarker := "DELETE FROM t WHERE email = '" + marker + "'"
	cases := []struct {
		name      string
		access    config.Access
		masked    bool
		sql       string
		wantErr   string
		violation string
	}{
		{"unmasked read-only read", config.ReadOnly, false, "SELECT 1", "", ""},
		{"unmasked read-only write", config.ReadOnly, false, write,
			"Refused (read-only profile): " + readonly.Violation(write), readonly.Violation(write)},
		{"unmasked read-write ignores a marker", config.ReadWrite, false, writeWithMarker, "", ""},
		{"unmasked read-only marker only hits the read-only screen", config.ReadOnly, false, "SELECT '" + marker + "'", "", ""},
		{"masked read-only refuses a marker", config.ReadOnly, true, "SELECT '" + marker + "'",
			"Refused (masked profile): " + markerReason, ""},
		{"masked read-write refuses a marker", config.ReadWrite, true, writeWithMarker,
			"Refused (masked profile): " + markerReason, ""},
		{"marker screen runs before the access screen", config.ReadOnly, true, writeWithMarker,
			"Refused (masked profile): " + markerReason, readonly.Violation(writeWithMarker)},
		{"masked read-only without a marker keeps the access screen", config.ReadOnly, true, write,
			"Refused (read-only profile): " + readonly.Violation(write), readonly.Violation(write)},
		{"masked read-write without a marker runs", config.ReadWrite, true, write, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mm *mask.Masker
			if c.masked {
				mm = m
			}
			violation := c.violation
			if violation == "" && c.access == config.ReadOnly {
				violation = readonly.Violation(c.sql)
			}
			err := guard(c.access, mm, violation, c.sql)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != c.wantErr {
				t.Errorf("got %q, want %q", got, c.wantErr)
			}
		})
	}
}

func TestMarkerScreenChecksEveryText(t *testing.T) {
	m := testMasker(t)
	marker := markerOf(t, m, "phone", "+4915112345678")
	err := markerScreen(m, "ok", "", `{"a": "`+marker+`"}`)
	want := "Refused (masked profile): " + mask.MarkerViolation(marker)
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	if err := markerScreen(m, "ok", "{}"); err != nil {
		t.Errorf("no marker: %v", err)
	}
	if err := markerScreen(nil, marker); err != nil {
		t.Errorf("unmasked profile must not screen markers: %v", err)
	}
}

func TestMaskErr(t *testing.T) {
	m := testMasker(t)
	marker := markerOf(t, m, "email", "ivan@example.com")
	refusal := errors.New("Refused (masked profile): " + mask.MarkerViolation(marker))
	cases := []struct {
		name string
		m    *mask.Masker
		err  error
		want string
	}{
		{"nil error", m, nil, ""},
		{"unmasked is untouched", nil, errors.New("Key (email)=(ivan@example.com) already exists"),
			"Key (email)=(ivan@example.com) already exists"},
		{"masked quotes a marker", m, errors.New("Key (email)=(ivan@example.com) already exists"),
			"Key (email)=(" + marker + ") already exists"},
		{"refusal text survives", m, refusal, refusal.Error()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := maskErr(c.m, c.err)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestMaskRedis(t *testing.T) {
	m := testMasker(t)
	email := markerOf(t, m, "email", "ivan@example.com")
	cases := []struct {
		name    string
		command string
		in      any
		want    any
	}{
		{"hash reply by field name", "HGETALL user:1",
			map[string]any{"email": "ivan@example.com", "plan": "pro"},
			map[string]any{"email": email, "plan": "pro"}},
		{"bare string by value rules", "GET user:1:contact", "ivan@example.com", email},
		{"bare string unchanged", "GET greeting", "hello", "hello"},
		{"list of strings", "LRANGE log 0 -1", []any{"ivan@example.com", "ok"}, []any{email, "ok"}},
		{"keys through Text", "keys *", []any{"user:ivan@example.com", "user:2"},
			[]any{"user:" + email, "user:2"}},
		{"scan reply through Text", "SCAN 0", []any{"0", []any{"user:ivan@example.com"}},
			[]any{"0", []any{"user:" + email}}},
		{"integer", "DBSIZE", int64(3), int64(3)},
		{"nil", "GET missing", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := maskRedis(m, c.command, c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %#v, want %#v", got, c.want)
			}
		})
	}
	in := map[string]any{"email": "ivan@example.com"}
	if got := maskRedis(nil, "HGETALL k", in); !reflect.DeepEqual(got, any(in)) {
		t.Errorf("unmasked result changed: %#v", got)
	}
}

func TestMaskMongo(t *testing.T) {
	m := testMasker(t)
	email := markerOf(t, m, "email", "ivan@example.com")
	docs := []any{
		map[string]any{"title": "a", "email": "ivan@example.com", "contact": map[string]any{"phone": "+4915112345678"}},
		map[string]any{"title": "b"},
	}
	out, fields := maskMongo(m, "find", "", docs)
	if want := []string{"contact.phone", "email"}; !reflect.DeepEqual(fields, want) {
		t.Errorf("fields = %v, want %v", fields, want)
	}
	first := out[0].(map[string]any)
	if first["email"] != email || first["title"] != "a" {
		t.Errorf("first = %#v", first)
	}
	if docs[0].(map[string]any)["email"] != "ivan@example.com" {
		t.Error("input documents must not be modified")
	}

	dist, fields := maskMongo(m, "distinct", "email", []any{"ivan@example.com", "x"})
	xMarker := markerOf(t, m, "email", "x")
	if !reflect.DeepEqual(dist, []any{email, xMarker}) || !reflect.DeepEqual(fields, []string{"email"}) {
		t.Errorf("distinct = %#v fields %v", dist, fields)
	}
	dist, fields = maskMongo(m, "distinct", "title", []any{"a", "b"})
	if !reflect.DeepEqual(dist, []any{"a", "b"}) || fields != nil {
		t.Errorf("distinct on a clear field = %#v fields %v", dist, fields)
	}

	if out, fields := maskMongo(nil, "find", "", docs); !reflect.DeepEqual(out, docs) || fields != nil {
		t.Errorf("unmasked = %#v %v", out, fields)
	}
}

// writeConfig points HQDB_CONFIG at a config with the given TOML.
func writeConfig(t *testing.T, toml string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hqdb.toml")
	if err := os.WriteFile(p, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HQDB_CONFIG", p)
}

func TestListProfilesReportsMasked(t *testing.T) {
	writeConfig(t, `
default_profile = "a"
[profiles.a]
adapter = "postgres"
[profiles.b]
adapter = "postgres"
mask = true
`)
	_, out, err := listProfiles(context.Background(), nil, listProfilesIn{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range out.Profiles {
		got[p.Name] = p.Masked
	}
	if want := map[string]bool{"a": false, "b": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("masked = %v, want %v", got, want)
	}
}

func TestMaskerFor(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *config.Config
		profile map[string]any
		wantNil bool
		wantErr string
	}{
		{"unmasked", &config.Config{}, map[string]any{"adapter": "postgres"}, true, ""},
		{"masked", &config.Config{}, map[string]any{"mask": true}, false, ""},
		{"masked with a masking table", &config.Config{Masking: map[string]any{"columns": []any{"name"}}},
			map[string]any{"mask": true}, false, ""},
		{"bad mask value", &config.Config{}, map[string]any{"mask": "yes"}, true, `option "mask" must be a boolean`},
		{"bad masking table", &config.Config{Masking: map[string]any{"nope": 1}}, map[string]any{"mask": true}, true,
			`unknown option "nope" in [masking]; valid options: columns, allow`},
		{"bad masking table is ignored when unmasked", &config.Config{Masking: map[string]any{"nope": 1}},
			map[string]any{}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := maskerFor(c.cfg, c.profile)
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr {
					t.Fatalf("got error %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (m == nil) != c.wantNil {
				t.Errorf("masker nil = %v, want %v", m == nil, c.wantNil)
			}
		})
	}
}

func TestMaskKeyIsSharedAcrossMaskers(t *testing.T) {
	old := maskKey
	t.Cleanup(func() { maskKey = old })
	maskKey = mask.NewKey()
	cfg := &config.Config{}
	profile := map[string]any{"mask": true}
	m1, _ := maskerFor(cfg, profile)
	m2, _ := maskerFor(cfg, profile)
	a, _ := m1.Value("email", "ivan@example.com")
	b, _ := m2.Value("contact", "IVAN@example.com")
	if a != b {
		t.Errorf("one session key must link values across calls: %v vs %v", a, b)
	}
	maskKey = mask.NewKey()
	m3, _ := maskerFor(cfg, profile)
	if c, _ := m3.Value("email", "ivan@example.com"); c == a {
		t.Error("a new key must give a different marker")
	}
}
