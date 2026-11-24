// Package setup moves literal Profile credentials into a secret store
// and rewrites the config to read them back through a ${cmd:...} or
// ${env:...} reference (ADR-0008). The stores are the platform's own,
// written through its CLI (ADR-0009): the Secret Service through
// secret-tool on Linux, the macOS Keychain through security; pass and the
// env file are offered where the shell can run or source them, so Windows
// gets neither of hqdb's own, and reading its Credential Manager is not
// something hqdb does.
// "hqdb setup" is the wizard over this package; every step
// is a plain function so agents can drive it too.
package setup

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/secrets"
)

// SecretKind says where in a Profile a literal credential lives.
type SecretKind string

const (
	// KindPassword: the password key, a literal string.
	KindPassword SecretKind = "password"
	// KindURL: a password inside a URL-form conn_str.
	KindURL SecretKind = "conn_str"
	// KindODBC: a Pwd= or Password= value inside an ODBC conn_str.
	KindODBC SecretKind = "conn_str (odbc)"
)

// Secret is one literal credential found in a Profile.
type Secret struct {
	Profile string
	Kind    SecretKind
	// Value is the literal secret; it is never rendered anywhere.
	Value string
	// File is the config file that contributes the Profile, the one a
	// patch must edit.
	File string
}

// Find returns every literal credential in cfg, Profile names in sorted
// order. A value that already carries a ${env:...} or ${cmd:...}
// reference is not reported: it is secured by its own definition.
func Find(cfg *config.Config) ([]Secret, error) {
	// A Profile is contributed by the last file defining its table, so
	// remember that file per name while walking the merge order.
	contrib := map[string]string{}
	paths, err := config.FilePaths()
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		doc, err := config.FileDoc(path)
		if err != nil {
			return nil, err
		}
		profiles, _ := doc["profiles"].(map[string]any)
		for name := range profiles {
			contrib[name] = path
		}
	}
	var out []Secret
	for _, name := range config.SortedProfileNames(cfg) {
		s, ok := literalSecret(name, contrib[name], cfg.Profiles[name])
		if ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// literalSecret reports the literal credential of one Profile table.
func literalSecret(name, file string, p map[string]any) (Secret, bool) {
	if pw, ok := p["password"].(string); ok && pw != "" && !secrets.HasReference(pw) {
		return Secret{Profile: name, Kind: KindPassword, Value: pw, File: file}, true
	}
	cs, err := config.OptConnStr(p)
	if err != nil || cs == "" || secrets.HasReference(cs) {
		return Secret{}, false
	}
	if pw, ok := urlPassword(cs); ok {
		return Secret{Profile: name, Kind: KindURL, Value: pw, File: file}, true
	}
	if pw, ok := odbcPassword(cs); ok {
		return Secret{Profile: name, Kind: KindODBC, Value: pw, File: file}, true
	}
	return Secret{}, false
}

// HasLiteral reports whether one Profile table carries a literal
// credential; hqdb check points at "hqdb setup" with it.
func HasLiteral(p map[string]any) bool {
	_, ok := literalSecret("", "", p)
	return ok
}

// urlPassword returns the password inside a URL-form conn_str.
func urlPassword(cs string) (string, bool) {
	isURL := false
	for _, s := range config.URLSchemes {
		if strings.HasPrefix(cs, s) {
			isURL = true
			break
		}
	}
	if !isURL {
		return "", false
	}
	u, err := url.Parse(cs)
	if err != nil || u.User == nil {
		return "", false
	}
	pw, ok := u.User.Password()
	return pw, ok && pw != ""
}

// odbcPassRe matches an ODBC password key with a non-empty value.
var odbcPassRe = regexp.MustCompile(`(?i)\b(pwd|password)\s*=\s*(\{[^}]*\}|[^;}]+)`)

// odbcPassword returns the value of a Pwd= or Password= key in an ODBC
// conn_str, braced values unbraced.
func odbcPassword(cs string) (string, bool) {
	m := odbcPassRe.FindStringSubmatch(cs)
	if m == nil {
		return "", false
	}
	v := strings.TrimSpace(m[2])
	v = strings.TrimSuffix(strings.TrimPrefix(v, "{"), "}")
	if v == "" {
		return "", false
	}
	return v, true
}
