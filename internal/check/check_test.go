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
		"bad    postgres  -           FAIL  unknown option \"nope\" for adapter \"postgres\"; valid options: access, adapter, conn_str, database, dbname, host, password, port, sslmode, user",
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

func TestRunUnknownProfile(t *testing.T) {
	var out bytes.Buffer
	if Run(context.Background(), testConfig(), Options{Profiles: []string{"nope"}}, &out) {
		t.Error("an unknown profile must fail")
	}
	if !strings.Contains(out.String(), `unknown profile "nope"`) {
		t.Errorf("got:\n%s", out.String())
	}
}
