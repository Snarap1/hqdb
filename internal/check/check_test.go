package check

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Snarap1/hqdb/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		DefaultProfile: "pg",
		Profiles: map[string]map[string]any{
			"pg":    {"adapter": "postgres", "host": "127.0.0.1"},
			"cache": {"adapter": "redis", "access": "read-write"},
			"bad":   {"adapter": "postgres", "nope": int64(1)},
		},
	}
}

func TestRunWithoutProbesReportsEveryProfile(t *testing.T) {
	var out bytes.Buffer
	ok := Run(context.Background(), testConfig(), Options{}, &out)
	if ok {
		t.Error("a config with an invalid profile must not pass")
	}
	want := []string{
		"bad    postgres  -           FAIL  unknown option \"nope\" for adapter \"postgres\"; valid options: access, adapter, conn_str, database, dbname, host, mask, password, port, sslmode, user",
		"cache  redis     read-write  ok",
		"pg     postgres  read-only   ok    (default)",
	}
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if strings.Join(got[:3], "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), strings.Join(want, "\n"))
	}
}

func TestRunSelectsProfiles(t *testing.T) {
	var out bytes.Buffer
	ok := Run(context.Background(), testConfig(), Options{Profiles: []string{"cache"}}, &out)
	if !ok {
		t.Errorf("only the valid profile was selected, want a pass:\n%s", out.String())
	}
	if strings.Contains(out.String(), "bad") {
		t.Errorf("unselected profile reported:\n%s", out.String())
	}
}

func TestRunValidatesMaskAndMaskingTable(t *testing.T) {
	cases := []struct {
		name     string
		profile  map[string]any
		masking  map[string]any
		wantPass bool
		want     []string
	}{
		{"mask true and no table", map[string]any{"adapter": "postgres", "mask": true}, nil, true,
			[]string{"p  postgres  read-only  ok"}},
		{"mask not a boolean", map[string]any{"adapter": "postgres", "mask": "yes"}, nil, false,
			[]string{`p  postgres  -  FAIL  option "mask" must be a boolean`}},
		{"valid masking table", map[string]any{"adapter": "postgres", "mask": true},
			map[string]any{"columns": []any{"name"}, "allow": []any{"*_verified"}}, true,
			[]string{"p          postgres  read-only  ok", "[masking]  -         -          ok"}},
		{"unknown masking key", map[string]any{"adapter": "postgres"},
			map[string]any{"region": "eu"}, false,
			[]string{`[masking]  -         -          FAIL  unknown option "region" in [masking]; valid options: columns, allow`}},
		{"malformed glob", map[string]any{"adapter": "postgres"},
			map[string]any{"columns": []any{"[a"}}, false, []string{"[masking]  -         -          FAIL"}},
		{"columns not strings", map[string]any{"adapter": "postgres"},
			map[string]any{"columns": "name"}, false, []string{"[masking]  -         -          FAIL"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{Profiles: map[string]map[string]any{"p": c.profile}, Masking: c.masking}
			var out bytes.Buffer
			if got := Run(context.Background(), cfg, Options{}, &out); got != c.wantPass {
				t.Errorf("pass = %v, want %v\n%s", got, c.wantPass, out.String())
			}
			for _, w := range c.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
		})
	}
}

func TestRunWithoutMaskingTableHasNoMaskingLine(t *testing.T) {
	var out bytes.Buffer
	Run(context.Background(), testConfig(), Options{}, &out)
	if strings.Contains(out.String(), "[masking]") {
		t.Errorf("unexpected masking line:\n%s", out.String())
	}
}

func TestRunUnknownProfile(t *testing.T) {
	var out bytes.Buffer
	if Run(context.Background(), testConfig(), Options{Profiles: []string{"nope"}}, &out) {
		t.Error("an unknown profile must fail")
	}
	if !strings.Contains(out.String(), `unknown profile "nope"`) {
		t.Errorf("got:\n%s", out.String())
	}
}

func TestRunChecksSecretReferencesWithoutExecuting(t *testing.T) {
	// --no-probe reports a missing env var and an empty command, and never
	// runs a ${cmd:...} reference.
	cases := []struct {
		name    string
		profile map[string]any
		want    string
	}{
		{"missing env var", map[string]any{"adapter": "postgres",
			"password": "${env:HQDB_CHECK_TEST_MISSING}"},
			`secret reference ${env:HQDB_CHECK_TEST_MISSING}: environment variable "HQDB_CHECK_TEST_MISSING" is not set`},
		{"empty command", map[string]any{"adapter": "redis",
			"password": "${cmd:}"},
			`secret reference ${cmd:}: command is empty`},
		{"set env var", map[string]any{"adapter": "postgres",
			"password": "${env:HQDB_CHECK_TEST_SET}"}, ""},
	}
	t.Setenv("HQDB_CHECK_TEST_SET", "v")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{Profiles: map[string]map[string]any{"p": c.profile}}
			var out bytes.Buffer
			ok := Run(context.Background(), cfg, Options{}, &out)
			if c.want == "" {
				if !ok || !strings.Contains(out.String(), "ok") {
					t.Errorf("a valid reference must pass:\n%s", out.String())
				}
				return
			}
			if ok || !strings.Contains(out.String(), c.want) {
				t.Errorf("got pass=%v, output:\n%s\nwant FAIL with %q", ok, out.String(), c.want)
			}
		})
	}
}

func TestRunHintsAtSetupForLiteralCredentials(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"p": {"adapter": "postgres", "password": "plain"},
	}}
	var out bytes.Buffer
	if ok := Run(context.Background(), cfg, Options{}, &out); !ok {
		t.Errorf("a literal password is valid, want a pass:\n%s", out.String())
	}
	want := "p  postgres  read-only  ok  literal credentials; run hqdb setup"
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")[0]
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
