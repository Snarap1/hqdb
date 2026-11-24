package secrets

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExpandLiteral(t *testing.T) {
	// Only a full ${env:...} or ${cmd:...} form is a reference; everything
	// else is literal, so a password written in the file keeps working.
	literals := []string{
		"",
		"no reference at all",
		"$HOME",
		"{env:HOME}",
		"${HOME}",
		"${env HOME}",
		"${env:unclosed",
		"${cmd:unclosed",
		"${vault:prod/password}",
	}
	for _, s := range literals {
		got, err := Expand(context.Background(), s)
		if err != nil {
			t.Errorf("Expand(%q) = %v, want no error", s, err)
		}
		if got != s {
			t.Errorf("Expand(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("HQDB_SECRET_TEST_V", "v")
	cases := []struct {
		in, want string
	}{
		{"${env:HQDB_SECRET_TEST_V}", "v"},
		{"postgres://u:${env:HQDB_SECRET_TEST_V}@h/db", "postgres://u:v@h/db"},
		{"${env:HQDB_SECRET_TEST_V}${env:HQDB_SECRET_TEST_V}", "vv"},
		{"pre ${env:HQDB_SECRET_TEST_V} post", "pre v post"},
	}
	for _, c := range cases {
		got, err := Expand(context.Background(), c.in)
		if err != nil {
			t.Errorf("Expand(%q) = %v, want no error", c.in, err)
		}
		if got != c.want {
			t.Errorf("Expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandEnvErrors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"${env:HQDB_SECRET_TEST_MISSING}", `secret reference ${env:HQDB_SECRET_TEST_MISSING}: environment variable "HQDB_SECRET_TEST_MISSING" is not set`},
		{"${env:}", `secret reference ${env:}: variable name is empty`},
		{"${cmd:}", `secret reference ${cmd:}: command is empty`},
	}
	for _, c := range cases {
		_, err := Expand(context.Background(), c.in)
		if err == nil {
			t.Errorf("Expand(%q) = no error, want %q", c.in, c.want)
			continue
		}
		if err.Error() != c.want {
			t.Errorf("Expand(%q) = %q, want %q", c.in, err, c.want)
		}
	}
}

func TestExpandCmd(t *testing.T) {
	// echo works under both sh and cmd; the trailing newline is trimmed.
	got, err := Expand(context.Background(), "${cmd:echo secret}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret" {
		t.Errorf("got %q, want %q", got, "secret")
	}
}

func TestExpandCmdFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh stderr redirection syntax")
	}
	_, err := Expand(context.Background(), "${cmd:echo boom >&2; exit 3}")
	want := "secret reference ${cmd:echo boom >&2; exit 3}: command failed: exit status 3: boom"
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

func TestExpandCmdCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Expand(ctx, "${cmd:echo canceled}")
	if err == nil {
		t.Fatal("a canceled context must fail the command")
	}
	if !strings.Contains(err.Error(), "command failed") {
		t.Errorf("got %v, want a command failure", err)
	}
}

func TestExpandCmdCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh command chaining syntax")
	}
	f := filepath.Join(t.TempDir(), "calls")
	cmd := "${cmd:echo x >> " + f + "; echo v}"
	got, err := Expand(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Errorf("got %q, want %q", got, "v")
	}
	if _, err := Expand(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if n := countLines(t, f); n != 1 {
		t.Errorf("command ran %d times, want 1 (cached)", n)
	}
	// An expired entry runs the command again.
	cacheMu.Lock()
	for k, e := range cache {
		cache[k] = cacheEntry{val: e.val, at: now().Add(-cacheTTL - time.Second)}
	}
	cacheMu.Unlock()
	if _, err := Expand(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if n := countLines(t, f); n != 2 {
		t.Errorf("command ran %d times after expiry, want 2", n)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(strings.TrimRight(string(data), "\n"), "\n") + 1
}

func TestResolve(t *testing.T) {
	t.Setenv("HQDB_SECRET_TEST_P", "pw")
	p := map[string]any{
		"host":     "127.0.0.1",
		"port":     int64(5432),
		"password": "${env:HQDB_SECRET_TEST_P}",
		"conn_str": []any{"Server=127.0.0.1,1433;", "Pwd=${env:HQDB_SECRET_TEST_P};"},
		"mask":     true,
	}
	got, err := Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if v := got["password"]; v != "pw" {
		t.Errorf("password = %v, want pw", v)
	}
	if _, ok := got["port"].(int64); !ok {
		t.Errorf("port = %v, want the int64 copied as it is", got["port"])
	}
	if _, ok := got["mask"].(bool); !ok {
		t.Errorf("mask = %v, want the bool copied as it is", got["mask"])
	}
	list, ok := got["conn_str"].([]any)
	if !ok || len(list) != 2 || list[1] != "Pwd=pw;" {
		t.Errorf("conn_str = %v, want the list elements expanded", got["conn_str"])
	}
	// The input table is never modified: the config's raw tables stay
	// comparable for the MCP pool's hash (ADR-0006).
	if p["password"] != "${env:HQDB_SECRET_TEST_P}" {
		t.Errorf("input profile was modified: %v", p["password"])
	}
	if l := p["conn_str"].([]any); l[1] != "Pwd=${env:HQDB_SECRET_TEST_P};" {
		t.Errorf("input list was modified: %v", l[1])
	}
}

func TestResolveError(t *testing.T) {
	p := map[string]any{"password": "${env:HQDB_SECRET_TEST_MISSING}"}
	if _, err := Resolve(context.Background(), p); err == nil {
		t.Fatal("a broken reference must fail the whole profile")
	}
}

func TestCheck(t *testing.T) {
	t.Setenv("HQDB_SECRET_TEST_V", "v")
	cases := []struct {
		name    string
		profile map[string]any
		want    string // "" for a pass
	}{
		{"literal password", map[string]any{"password": "plain"}, ""},
		{"set env", map[string]any{"password": "${env:HQDB_SECRET_TEST_V}"}, ""},
		{"unset env", map[string]any{"password": "${env:HQDB_SECRET_TEST_MISSING}"},
			`secret reference ${env:HQDB_SECRET_TEST_MISSING}: environment variable "HQDB_SECRET_TEST_MISSING" is not set`},
		{"empty command", map[string]any{"password": "${cmd:}"},
			`secret reference ${cmd:}: command is empty`},
		{"env reference in a list", map[string]any{"conn_str": []any{"Pwd=${env:HQDB_SECRET_TEST_MISSING};"}},
			`secret reference ${env:HQDB_SECRET_TEST_MISSING}: environment variable "HQDB_SECRET_TEST_MISSING" is not set`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Check(c.profile)
			if c.want == "" {
				if err != nil {
					t.Errorf("Check = %v, want a pass", err)
				}
				return
			}
			if err == nil || err.Error() != c.want {
				t.Errorf("Check = %v, want %q", err, c.want)
			}
		})
	}
}

func TestCheckNeverRunsCommands(t *testing.T) {
	f := filepath.Join(t.TempDir(), "proof")
	if err := Check(map[string]any{"password": "${cmd:touch " + f + "}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); err == nil {
		t.Error("Check executed the command; it must validate without running")
	}
}
