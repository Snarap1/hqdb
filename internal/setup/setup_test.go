package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Snarap1/hqdb/internal/config"
)

// loadFrom writes text as the only config file in an isolated HOME and
// loads it, so Find sees a single contributing file.
func loadFrom(t *testing.T, text string) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	path := filepath.Join(home, "hqdb.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HQDB_CONFIG", path)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg, path
}

func TestFind(t *testing.T) {
	const text = `default_profile = "prod"

# a comment that must survive
[profiles.prod]
adapter = "postgres"
host = "db"
password = "literal"          # inline comment

[profiles.ref]
adapter = "postgres"
password = "${cmd:pass show hqdb/ref}"

[profiles.empty]
adapter = "postgres"
password = ""

[profiles.url]
adapter = "postgres"
conn_str = "postgres://u:p%40ss@127.0.0.1:5432/db"

[profiles.nopass]
adapter = "postgres"
conn_str = "postgres://u@127.0.0.1:5432/db"

[profiles.local-odbc]
adapter = "odbc"
conn_str = [
  "Driver={ODBC Driver 18 for SQL Server};",
  "Server=127.0.0.1,51433;",
  "Pwd=odbc-secret;",
]
`
	cfg, path := loadFrom(t, text)
	got, err := Find(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []Secret{
		{Profile: "local-odbc", Kind: KindODBC, Value: "odbc-secret", File: path},
		{Profile: "prod", Kind: KindPassword, Value: "literal", File: path},
		{Profile: "url", Kind: KindURL, Value: "p@ss", File: path},
	}
	if len(got) != len(want) {
		t.Fatalf("Find = %d secrets (%+v), want %d", len(got), got, len(want))
	}
	// FilePaths may surface the same file twice through discovery when
	// HOME and cwd overlap; the profiles and kinds must match regardless.
	for i := range want {
		if got[i].Profile != want[i].Profile || got[i].Kind != want[i].Kind || got[i].Value != want[i].Value {
			t.Errorf("secret %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestHasLiteral(t *testing.T) {
	cases := []struct {
		name string
		p    map[string]any
		want bool
	}{
		{"literal password", map[string]any{"password": "x"}, true},
		{"referenced password", map[string]any{"password": "${env:X}"}, false},
		{"empty password", map[string]any{"password": ""}, false},
		{"url with password", map[string]any{"conn_str": "postgres://u:p@h/db"}, true},
		{"odbc password", map[string]any{"conn_str": []any{"Pwd=x;"}}, true},
		{"none", map[string]any{"host": "h"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasLiteral(c.p); got != c.want {
				t.Errorf("HasLiteral = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPatchPassword(t *testing.T) {
	const text = `default_profile = "prod"

# the header comment survives
[profiles.prod]
adapter = "postgres"
host = "db"                  # and this one
password = "literal"          # inline comment survives

[profiles.other]
adapter = "redis"
password = "keep-me"
`
	path := filepath.Join(t.TempDir(), "hqdb.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := "${cmd:pass show hqdb/prod}"
	if err := Patch(path, "prod", KindPassword, "literal", ref); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(after)
	for _, want := range []string{
		`password = "${cmd:pass show hqdb/prod}"          # inline comment survives`,
		"# the header comment survives",
		`host = "db"                  # and this one`,
		`password = "keep-me"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("patched file lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "literal") {
		t.Errorf("the literal survived:\n%s", got)
	}
}

func TestPatchQuotedHeaderAndProfile(t *testing.T) {
	const text = "[profiles.\"my prod\"]\npassword = \"s3cret\"\n"
	path := filepath.Join(t.TempDir(), "hqdb.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Patch(path, "my prod", KindPassword, "s3cret", "${env:HQDB_MY_PROD_PASSWORD}"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `password = "${env:HQDB_MY_PROD_PASSWORD}"`) {
		t.Errorf("got:\n%s", got)
	}
}

func TestPatchURLPassword(t *testing.T) {
	const text = "[profiles.url]\nconn_str = \"postgres://u:p%40ss@127.0.0.1:5432/db\"\n"
	path := filepath.Join(t.TempDir(), "hqdb.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Patch(path, "url", KindURL, "p@ss", "${env:HQDB_URL_PASSWORD}"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := `conn_str = "postgres://u:${env:HQDB_URL_PASSWORD}@127.0.0.1:5432/db"`
	if !strings.Contains(string(got), want) {
		t.Errorf("got:\n%s\nwant %q", got, want)
	}
}

func TestPatchODBCListElement(t *testing.T) {
	const text = `[profiles.odbc]
adapter = "odbc"
conn_str = [
  "Driver={ODBC Driver 18 for SQL Server};",
  "Server=127.0.0.1,51433;",
  "Pwd=odbc-secret;",
]
`
	path := filepath.Join(t.TempDir(), "hqdb.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Patch(path, "odbc", KindODBC, "odbc-secret", "${cmd:pass show hqdb/odbc}"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := `"Pwd=${cmd:pass show hqdb/odbc};",`
	if !strings.Contains(string(got), want) {
		t.Errorf("got:\n%s", got)
	}
	// The list is still a list: the parsed value joins with the space.
	doc, err := config.FileDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	p := doc["profiles"].(map[string]any)["odbc"].(map[string]any)
	cs, err := config.OptConnStr(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cs, "${cmd:pass show hqdb/odbc}") {
		t.Errorf("conn_str = %q, want the reference joined in", cs)
	}
}

func TestPatchRefusesWhatItCannotRewrite(t *testing.T) {
	cases := []struct {
		name, text, profile string
		kind                SecretKind
		secret              string
		want                string
	}{
		{"multiline password", "[profiles.p]\npassword = \"\"\"li\nteral\"\"\"\n", "p", KindPassword, "li\nteral",
			"the password value of profile \"p\" could not be rewritten safely (multiline, escaped, or no longer the literal value); edit it by hand"},
		{"already referenced", "[profiles.p]\npassword = \"${env:X}\"\n", "p", KindPassword, "old",
			"the password value of profile \"p\" could not be rewritten safely (multiline, escaped, or no longer the literal value); edit it by hand"},
		{"missing profile", "[profiles.other]\npassword = \"x\"\n", "p", KindPassword, "x",
			`could not find profile "p"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hqdb.toml")
			if err := os.WriteFile(path, []byte(c.text), 0o600); err != nil {
				t.Fatal(err)
			}
			err := Patch(path, c.profile, c.kind, c.secret, "${env:HQDB_P_PASSWORD}")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.HasPrefix(err.Error(), c.want) && !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %q, want %q", err, c.want)
			}
			// Nothing was written.
			got, _ := os.ReadFile(path)
			if string(got) != c.text {
				t.Errorf("the file changed on a refusal:\n%s", got)
			}
		})
	}
}

func TestPatchKeepsOtherTopLevelValues(t *testing.T) {
	const text = `default_profile = "p"

[profiles.p]
password = "x"

[masking]
columns = ["name"]
`
	path := filepath.Join(t.TempDir(), "hqdb.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Patch(path, "p", KindPassword, "x", "${env:HQDB_P_PASSWORD}"); err != nil {
		t.Fatal(err)
	}
	doc, err := config.FileDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc["default_profile"] != "p" {
		t.Errorf("default_profile = %v, want p", doc["default_profile"])
	}
	m, _ := doc["masking"].(map[string]any)
	if !reflect.DeepEqual(m, map[string]any{"columns": []any{"name"}}) {
		t.Errorf("masking = %#v, want untouched", m)
	}
}

func TestStoresPass(t *testing.T) {
	stores := Stores("")
	var pass *Store
	for i := range stores {
		if stores[i].ID == "pass" {
			pass = &stores[i]
		}
	}
	if pass == nil {
		t.Fatal("pass store missing")
	}
	if got := pass.Reference("prod"); got != "${cmd:pass show hqdb/prod}" {
		t.Errorf("reference = %q", got)
	}
	var ranCmd string
	var ranArgs []string
	var ranStdin string
	run := func(command string, args []string, stdin string) (string, error) {
		ranCmd, ranArgs, ranStdin = command, args, stdin
		return "stored-secret\n", nil
	}
	if _, err := pass.Move(run, "prod", "stored-secret"); err != nil {
		t.Fatal(err)
	}
	if ranCmd != "pass" || !reflect.DeepEqual(ranArgs, []string{"insert", "-m", "-f", "hqdb/prod"}) || ranStdin != "stored-secret\n" {
		t.Errorf("Move ran %s %v with stdin %q, want pass insert -m -f over stdin", ranCmd, ranArgs, ranStdin)
	}
	if err := pass.Verify(run, "prod", "stored-secret"); err != nil {
		t.Errorf("Verify = %v, want a pass", err)
	}
	if err := pass.Verify(run, "prod", "other"); err == nil {
		t.Error("Verify must fail on a mismatch")
	} else if !strings.Contains(err.Error(), "did not read back") {
		t.Errorf("Verify = %v, want the read-back wording", err)
	}
}

func TestStoresEnvFile(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.sh")
	stores := Stores(envFile)
	var env *Store
	for i := range stores {
		if stores[i].ID == "env" {
			env = &stores[i]
		}
	}
	if got := env.Reference("my prod"); got != "${env:HQDB_MY_PROD_PASSWORD}" {
		t.Errorf("reference = %q", got)
	}
	notes, err := env.Move(nil, "my prod", "s3cr et")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], envFile) {
		t.Errorf("notes = %q, want the env file wiring", notes)
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "export HQDB_MY_PROD_PASSWORD='s3cr et'\n" {
		t.Errorf("env file = %q", data)
	}
	info, _ := os.Stat(envFile)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("env file mode = %v, want 0600", info.Mode().Perm())
	}
	// Re-running updates the same variable, not doubles the file.
	if _, err := env.Move(nil, "my prod", "rotated"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(envFile)
	if strings.Count(string(data), "HQDB_MY_PROD_PASSWORD") != 1 {
		t.Errorf("env file doubled:\n%s", data)
	}
	if !strings.Contains(string(data), "rotated") {
		t.Errorf("env file did not update:\n%s", data)
	}
}

func TestStoresSecretServiceOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the linux Secret Service store")
	}
	stores := Stores("")
	var os *Store
	for i := range stores {
		if stores[i].ID == "secret-service" {
			os = &stores[i]
		}
	}
	if os == nil {
		t.Fatal("secret-service store missing")
	}
	if got := os.Reference("prod"); got != "${cmd:secret-tool lookup service hqdb profile prod}" {
		t.Errorf("reference = %q", got)
	}
	var ranArgs []string
	var ranStdin string
	run := func(command string, args []string, stdin string) (string, error) {
		ranArgs, ranStdin = args, stdin
		return "s\n", nil
	}
	if _, err := os.Move(run, "prod", "s"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ranArgs, []string{"store", "--label=hqdb/prod", "service", "hqdb", "profile", "prod"}) || ranStdin != "s" {
		t.Errorf("Move ran %v with stdin %q, want the secret over stdin", ranArgs, ranStdin)
	}
	if err := os.Verify(run, "prod", "s"); err != nil {
		t.Errorf("Verify = %v", err)
	}
}

// TestStoresWindowsHasNoStore pins the decision in ADR-0009: hqdb does
// not read the Windows Credential Manager, because a binary that pulls a
// stored password out of the OS store and prints it is what an antivirus
// treats as an infostealer, and it writes no env file there either,
// because nothing on Windows sources one.
func TestStoresWindowsHasNoStore(t *testing.T) {
	for _, s := range stores("windows", "") {
		if s.Available {
			t.Errorf("store %q must not be offered on Windows", s.ID)
		}
		switch s.ID {
		case "keychain", "secret-service":
			t.Errorf("store %q must not exist on Windows", s.ID)
		}
	}
	if s, ok := osStore("windows"); ok {
		t.Fatalf("osStore(%q) = %q, want no store", "windows", s.ID)
	}
	// The POSIX platforms keep the env store, and pass wherever it is
	// installed.
	for _, goos := range []string{"linux", "darwin"} {
		for _, s := range stores(goos, "") {
			if s.ID == "env" && !s.Available {
				t.Errorf("stores(%q) must offer the env store", goos)
			}
		}
	}
}

func TestApplyEndToEnd(t *testing.T) {
	const text = `[profiles.prod]
adapter = "postgres"
password = "literal"

[profiles.keep]
adapter = "redis"
password = "keep-me"
`
	_, path := loadFrom(t, text)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	found, err := Find(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Both profiles carry literals; the item below moves only prod, so
	// keep must stay exactly as it was.
	var prod *Secret
	for i := range found {
		if found[i].Profile == "prod" {
			prod = &found[i]
		}
	}
	if prod == nil {
		t.Fatalf("Find = %+v, want prod", found)
	}
	envFile := filepath.Join(t.TempDir(), "env.sh")
	var env Store
	for _, s := range Stores(envFile) {
		if s.ID == "env" {
			env = s
		}
	}
	results := Apply([]Item{{Secret: *prod, Store: env}}, nil)
	if len(results) != 1 || !results[0].Ok {
		t.Fatalf("Apply = %+v", results)
	}
	if results[0].Reference != "${env:HQDB_PROD_PASSWORD}" {
		t.Errorf("reference = %q", results[0].Reference)
	}
	after, err := config.FileDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	p := after["profiles"].(map[string]any)["prod"].(map[string]any)
	if p["password"] != "${env:HQDB_PROD_PASSWORD}" {
		t.Errorf("prod password = %v", p["password"])
	}
	keep := after["profiles"].(map[string]any)["keep"].(map[string]any)
	if keep["password"] != "keep-me" {
		t.Errorf("keep password = %v, want untouched", keep["password"])
	}
}

func TestApplyStoreFailureLeavesConfig(t *testing.T) {
	const text = `[profiles.prod]
password = "literal"
`
	_, path := loadFrom(t, text)
	var pass Store
	for _, s := range Stores("") {
		if s.ID == "pass" {
			pass = s
		}
	}
	// The fake pass store fails to read the secret back.
	run := func(command string, args []string, stdin string) (string, error) {
		if len(args) > 0 && args[0] == "show" {
			return "not-the-secret", nil
		}
		return "", nil
	}
	results := Apply([]Item{{Secret: Secret{Profile: "prod", Kind: KindPassword, Value: "literal", File: path}, Store: pass}}, run)
	if len(results) != 1 || results[0].Ok {
		t.Fatalf("Apply = %+v, want a failure", results)
	}
	if !strings.Contains(results[0].Err.Error(), "did not read the secret back") {
		t.Errorf("err = %v", results[0].Err)
	}
	// The config still carries the literal: nothing was stranded.
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "${cmd:pass") {
		t.Errorf("the config was patched although the store failed:\n%s", got)
	}
}
