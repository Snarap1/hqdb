package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestOptConnStrListForm(t *testing.T) {
	// Real profiles use the list form; elements join with a single space.
	p := map[string]any{"conn_str": []any{"Driver={ODBC Driver 18 for SQL Server}", "Server=127.0.0.1,1433"}}
	got, err := OptConnStr(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "Driver={ODBC Driver 18 for SQL Server} Server=127.0.0.1,1433"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFillFromURL(t *testing.T) {
	p := map[string]any{"conn_str": "postgres://u:p@127.0.0.1:5432/d"}
	got, err := FillFromURL(p, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"host": "127.0.0.1", "user": "u", "password": "p", "dbname": "d",
	}
	for k, want := range checks {
		v, err := OptStr(got, k)
		if err != nil {
			t.Fatal(err)
		}
		if v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	if port, ok, _ := OptInt(got, "port"); !ok || port != 5432 {
		t.Errorf("port = %d (ok=%v), want 5432", port, ok)
	}
}

func TestFillFromURLExplicitKeyWins(t *testing.T) {
	p := map[string]any{"conn_str": "postgres://u:p@127.0.0.1:5432/d", "host": "explicit"}
	got, err := FillFromURL(p, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := OptStr(got, "host"); h != "explicit" {
		t.Errorf("host = %q, want explicit", h)
	}
}

func TestFillFromURLClickHouseDatabase(t *testing.T) {
	p := map[string]any{"conn_str": "clickhouse://hq:hq@127.0.0.1:8123/hqdb"}
	got, err := FillFromURL(p, "clickhouse")
	if err != nil {
		t.Fatal(err)
	}
	if db, _ := OptStr(got, "database"); db != "hqdb" {
		t.Errorf("database = %q, want hqdb", db)
	}
}

func TestValidateUnknownKey(t *testing.T) {
	p := map[string]any{"adapter": "postgres", "host": "127.0.0.1", "foo": int64(1)}
	_, err := Validate(p)
	if err == nil {
		t.Fatal("expected an error for an unknown profile key")
	}
	want := `unknown option "foo" for adapter "postgres"; valid options: access, adapter, conn_str, database, dbname, host, mask, password, port, sslmode, user`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestValidateUnsupportedAdapter(t *testing.T) {
	_, err := Validate(map[string]any{"adapter": "duckdb"})
	if err == nil {
		t.Fatal("expected an error for the duckdb adapter")
	}
	want := `adapter "duckdb" is not supported; supported: postgres, mysql, mssql, odbc, clickhouse, redis, mongodb`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestLoadConfigMerge(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("HQDB_CONFIG", "")

	// A higher-priority file replaces the whole profiles table.
	if err := os.WriteFile(filepath.Join(home, ".hqdb.toml"),
		[]byte("default_profile = \"one\"\n[profiles.one]\nadapter = \"postgres\"\nhost = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "hqdb.toml"),
		[]byte("default_profile = \"two\"\n[profiles.two]\nadapter = \"redis\"\nhost = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(cwd, "hqdb.toml")) })

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProfile != "two" {
		t.Errorf("default_profile = %q, want two (cwd wins over home)", cfg.DefaultProfile)
	}
	if _, ok := cfg.Profiles["one"]; ok {
		t.Error("profile one should be gone: the higher-priority file replaces the whole profiles table")
	}
	if _, ok := cfg.Profiles["two"]; !ok {
		t.Error("profile two is missing")
	}
}

func TestReadConfigFilePyproject(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pyproject.toml")
	if err := os.WriteFile(p, []byte("[project]\nname = \"x\"\n\n[tool.hqdb]\ndefault_profile = \"p\"\n[tool.hqdb.profiles.p]\nadapter = \"postgres\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := readConfigFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["project"]; ok {
		t.Error("the top-level [project] table must not leak in")
	}
	if doc["default_profile"] != "p" {
		t.Errorf("default_profile = %v, want p", doc["default_profile"])
	}
}

func TestLoadConfigRejectsUnknownTopLevelKey(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("HQDB_CONFIG", "")

	if err := os.WriteFile(filepath.Join(home, ".hqdb.toml"), []byte("proflies = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("expected an error for an unknown top-level key")
	}
}

func TestLoadConfigIgnoresOtherToolConfigs(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "other-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("HQDB_CONFIG", "")

	// A foreign CLI config (keymaps included) is invisible to hqdb.
	if err := os.WriteFile(filepath.Join(home, ".config", "other-cli", "config.toml"),
		[]byte("default_profile = \"hq\"\n[keymaps.global]\nquit = \"ctrl+q\"\n[profiles.hq]\nadapter = \"postgres\"\nhost = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// hqdb's own config dir is searched.
	if err := os.MkdirAll(filepath.Join(home, ".config", "hqdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "hqdb", "config.toml"),
		[]byte("default_profile = \"mine\"\n[profiles.mine]\nadapter = \"redis\"\nhost = \"127.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProfile != "mine" {
		t.Errorf("default_profile = %q, want mine", cfg.DefaultProfile)
	}
	if _, ok := cfg.Profiles["hq"]; ok {
		t.Error("profile from the foreign CLI config must be ignored")
	}
	if _, ok := cfg.Profiles["mine"]; !ok {
		t.Error("profile mine is missing")
	}
}

func TestProfileFallback(t *testing.T) {
	cfg := &Config{DefaultProfile: "def", Profiles: map[string]map[string]any{
		"def": {"adapter": "redis"},
	}}
	p, err := cfg.Profile("")
	if err != nil {
		t.Fatal(err)
	}
	if p["adapter"] != "redis" {
		t.Errorf("adapter = %v, want redis", p["adapter"])
	}
	if _, err := cfg.Profile("nope"); err == nil {
		t.Error("expected an error for an unknown profile")
	} else if err.Error() != `unknown profile "nope"; run list_profiles` {
		t.Errorf("unexpected message: %q", err)
	}
	empty := &Config{Profiles: map[string]map[string]any{}}
	if _, err := empty.Profile(""); err == nil ||
		err.Error() != "no profile specified and no default_profile is set" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTomlUnmarshalSanity(t *testing.T) {
	// Guards that profile values arrive as the types the accessors expect.
	var doc map[string]any
	if err := toml.Unmarshal([]byte("[profiles.p]\nadapter=\"redis\"\nport=6380\nsecure=true\n"), &doc); err != nil {
		t.Fatal(err)
	}
	p := doc["profiles"].(map[string]any)["p"].(map[string]any)
	if port, ok, err := OptInt(p, "port"); err != nil || !ok || port != 6380 {
		t.Errorf("port = %d (ok=%v, err=%v), want 6380", port, ok, err)
	}
	if secure, err := OptBool(p, "secure"); err != nil || !secure {
		t.Errorf("secure = %v (err=%v), want true", secure, err)
	}
}

func TestAccess(t *testing.T) {
	cases := []struct {
		name    string
		profile map[string]any
		want    Access
		wantErr string
	}{
		{"missing defaults to read-only", map[string]any{"adapter": "postgres"}, ReadOnly, ""},
		{"read-only", map[string]any{"adapter": "postgres", "access": "read-only"}, ReadOnly, ""},
		{"read-write", map[string]any{"adapter": "redis", "access": "read-write"}, ReadWrite, ""},
		{"unknown value", map[string]any{"adapter": "postgres", "access": "admin"}, ReadOnly,
			`option "access" must be "read-only" or "read-write" (got "admin")`},
		{"not a string", map[string]any{"adapter": "postgres", "access": true}, ReadOnly,
			`option "access" must be a string`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ProfileAccess(c.profile)
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr {
					t.Fatalf("got error %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestValidateAcceptsAccessOnEveryAdapter(t *testing.T) {
	for _, adapter := range strings.Split(SupportedAdapters, ", ") {
		if _, err := Validate(map[string]any{"adapter": adapter, "access": "read-write"}); err != nil {
			t.Errorf("%s: %v", adapter, err)
		}
	}
}

func TestValidateRejectsBadAccess(t *testing.T) {
	_, err := Validate(map[string]any{"adapter": "mysql", "access": "rw"})
	want := `option "access" must be "read-only" or "read-write" (got "rw")`
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

func TestProfileMask(t *testing.T) {
	cases := []struct {
		name    string
		profile map[string]any
		want    bool
		wantErr string
	}{
		{"missing defaults to false", map[string]any{"adapter": "postgres"}, false, ""},
		{"true", map[string]any{"adapter": "postgres", "mask": true}, true, ""},
		{"false", map[string]any{"adapter": "postgres", "mask": false}, false, ""},
		{"not a boolean", map[string]any{"adapter": "postgres", "mask": "yes"}, false,
			`option "mask" must be a boolean`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ProfileMask(c.profile)
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr {
					t.Fatalf("got error %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("got %v (err=%v), want %v", got, err, c.want)
			}
		})
	}
}

func TestValidateAcceptsMaskOnEveryAdapter(t *testing.T) {
	for _, adapter := range strings.Split(SupportedAdapters, ", ") {
		if _, err := Validate(map[string]any{"adapter": adapter, "mask": true}); err != nil {
			t.Errorf("%s: %v", adapter, err)
		}
	}
}

func TestValidateRejectsBadMask(t *testing.T) {
	_, err := Validate(map[string]any{"adapter": "mysql", "mask": "yes"})
	want := `option "mask" must be a boolean`
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

func TestLoadMaskingTable(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		want    map[string]any
		wantErr string
	}{
		{"absent", "[profiles.p]\nadapter = \"postgres\"\n", nil, ""},
		{"table kept raw", "[masking]\ncolumns = [\"name\"]\nallow = [\"*_verified\"]\n",
			map[string]any{"columns": []any{"name"}, "allow": []any{"*_verified"}}, ""},
		{"not a table", "masking = 1\n", nil, "masking must be a TOML table"},
		{"unknown top-level key", "proflies = 1\n", nil,
			"hqdb config files must only contain default_profile, profiles, and masking tables (got proflies)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("HQDB_CONFIG", "")
			if err := os.WriteFile(filepath.Join(home, ".hqdb.toml"), []byte(c.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr {
					t.Fatalf("got error %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.Masking, c.want) {
				t.Errorf("Masking = %#v, want %#v", cfg.Masking, c.want)
			}
		})
	}
}

func TestLoadMaskingReplacesWhole(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("HQDB_CONFIG", "")
	if err := os.WriteFile(filepath.Join(home, ".hqdb.toml"), []byte("[masking]\ncolumns = [\"a\"]\nallow = [\"b\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "hqdb.toml"), []byte("[masking]\ncolumns = [\"c\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"columns": []any{"c"}}
	if !reflect.DeepEqual(cfg.Masking, want) {
		t.Errorf("Masking = %#v, want %#v (top-level replacement)", cfg.Masking, want)
	}
}

func TestRedactConnStr(t *testing.T) {
	cases := []struct{ in, want string }{
		{"postgres://app:secret@127.0.0.1:5432/appdb", "postgres://***@127.0.0.1:5432/appdb"},
		{"postgres://app@127.0.0.1:5432/appdb", "postgres://***@127.0.0.1:5432/appdb"},
		{"Server=127.0.0.1,1433;Uid=sa;Pwd=secret;", "Server=127.0.0.1,1433;Uid=sa;Pwd=***;"},
		{"Server=127.0.0.1;Password={s e c r e t};", "Server=127.0.0.1;Password=***;"},
		{"Driver={ODBC Driver 18 for SQL Server};Server=127.0.0.1,1433;", "Driver={ODBC Driver 18 for SQL Server};Server=127.0.0.1,1433;"},
		{"no credentials here", "no credentials here"},
	}
	for _, c := range cases {
		if got := RedactConnStr(c.in); got != c.want {
			t.Errorf("RedactConnStr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFillFromURLParseErrorDoesNotLeakPassword(t *testing.T) {
	// A parse failure names the conn_str redacted, and drops url.Parse's
	// own error, which embeds the URL verbatim.
	p := map[string]any{"conn_str": "postgres://app:secret@127.0.0.1:notaport/appdb"}
	_, err := FillFromURL(p, "postgres")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks the password: %v", err)
	}
	want := `could not parse conn_str "postgres://***@127.0.0.1:notaport/appdb" as a URL`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}
