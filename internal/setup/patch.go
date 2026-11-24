package setup

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Snarap1/hqdb/internal/config"
)

// Patch rewrites one literal credential in the config file at path: the
// single line that carries it is replaced with the reference, and
// everything else in the file stays exactly as it was, comments and
// formatting included. The result is verified before the file is
// replaced: it must parse, the Profile must carry the reference where
// the credential lived, the old literal secret must be gone, and every
// other value must be unchanged. The write is atomic (a temp file in
// the same directory, then rename), so a failed patch never leaves a
// half-written config.
func Patch(path, profile string, kind SecretKind, secret, ref string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	patched, err := patchText(text, path, profile, kind, secret, ref)
	if err != nil {
		return err
	}
	if err := verifyPatch(text, patched, path, profile, kind, secret, ref); err != nil {
		return fmt.Errorf("%v; %s was not changed", err, filepath.Base(path))
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hqdb-patch-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(patched); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// patchText returns text with the one line carrying the credential
// rewritten to the reference.
func patchText(text, path, profile string, kind SecretKind, secret, ref string) (string, error) {
	lines := strings.Split(text, "\n")
	lo, hi := sectionRange(lines, path, profile)
	if lo < 0 {
		return "", fmt.Errorf("could not find profile %q in %s", profile, path)
	}
	found, refused := false, false
	for i := lo; i <= hi; i++ {
		if !lineCarries(lines[i], kind) {
			continue
		}
		found = true
		replaced, ok := rewriteQuoted(lines[i], func(inner string) (string, bool) {
			return replaceInner(inner, kind, secret, ref)
		})
		if !ok {
			refused = true
			continue
		}
		lines[i] = replaced
		return strings.Join(lines, "\n"), nil
	}
	switch {
	case refused:
		return "", fmt.Errorf("the %s value of profile %q could not be rewritten safely (multiline, escaped, or no longer the literal value); edit it by hand", kind, profile)
	case !found:
		return "", fmt.Errorf("could not find the %s line of profile %q in %s; it may span more than one line; edit it by hand", kind, profile, path)
	}
	return "", fmt.Errorf("could not rewrite the %s value of profile %q; edit it by hand", kind, profile)
}

// sectionRange returns the 0-based inclusive line indexes of the
// Profile's table body, or lo = -1 when its header is missing. The body
// ends at the next table header. In a pyproject.toml the header is
// [tool.hqdb.profiles.<name>].
func sectionRange(lines []string, path, profile string) (int, int) {
	prefix := "profiles."
	if filepath.Base(path) == "pyproject.toml" {
		prefix = "tool.hqdb.profiles."
	}
	start := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if start < 0 {
			if isProfileHeader(t, prefix, profile) {
				start = i
			}
			continue
		}
		if strings.HasPrefix(t, "[") {
			return start, i - 1
		}
	}
	if start < 0 {
		return -1, -1
	}
	return start, len(lines) - 1
}

// isProfileHeader reports whether the trimmed line is the Profile's
// table header, bare or quoted.
func isProfileHeader(t, prefix, profile string) bool {
	if !strings.HasPrefix(t, "["+prefix) || !strings.HasSuffix(t, "]") {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(t, "["+prefix), "]")
	return name == profile || name == `"`+profile+`"`
}

// lineCarries reports whether the line is the one the credential lives
// on: the password key, a URL-form conn_str key, or an ODBC string or
// list element holding a Pwd= or Password= value.
func lineCarries(line string, kind SecretKind) bool {
	t := strings.TrimSpace(line)
	key, _, hasEq := strings.Cut(t, "=")
	key = strings.TrimSpace(key)
	switch kind {
	case KindPassword:
		return hasEq && key == "password"
	case KindURL:
		return hasEq && key == "conn_str" && strings.Contains(t, "://")
	case KindODBC:
		if hasEq && key == "conn_str" {
			return odbcPassRe.MatchString(t)
		}
		// A list element is a bare quoted string, so it has no key.
		return strings.HasPrefix(t, `"`) && odbcPassRe.MatchString(t)
	}
	return false
}

// replaceInner rewrites the content of the quoted string that carries
// the credential.
func replaceInner(inner string, kind SecretKind, secret, ref string) (string, bool) {
	switch kind {
	case KindPassword:
		if inner != secret {
			return "", false
		}
		return ref, true
	case KindURL:
		return replaceURLPassword(inner, secret, ref)
	case KindODBC:
		if !strings.Contains(inner, secret) {
			return "", false
		}
		return strings.Replace(inner, secret, ref, 1), true
	}
	return "", false
}

// replaceURLPassword replaces the password in a URL's userinfo
// (user:pass before the first "@"), matching the secret through its
// percent-encoded form too.
func replaceURLPassword(s, secret, ref string) (string, bool) {
	i := strings.Index(s, "://")
	if i < 0 {
		return "", false
	}
	at := strings.Index(s[i+3:], "@")
	if at < 0 {
		return "", false
	}
	raw := s[i+3 : i+3+at]
	colon := strings.LastIndex(raw, ":")
	if colon < 0 {
		return "", false
	}
	pw := raw[colon+1:]
	if pw != secret {
		if dec, err := url.QueryUnescape(pw); err != nil || dec != secret {
			return "", false
		}
	}
	return s[:i+3+colon+1] + ref + s[i+3+at:], true
}

// rewriteQuoted replaces the content of the first double-quoted string
// on line, keeping everything around it (key, comma, trailing comment).
// Escaped or multiline strings are refused rather than guessed at.
func rewriteQuoted(line string, f func(inner string) (string, bool)) (string, bool) {
	if strings.Contains(line, `"""`) || strings.Contains(line, `'''`) {
		return "", false
	}
	start := strings.IndexByte(line, '"')
	if start < 0 {
		return "", false
	}
	end := strings.IndexByte(line[start+1:], '"')
	if end < 0 {
		return "", false
	}
	end += start + 1
	inner := line[start+1 : end]
	if strings.Contains(inner, `\`) {
		return "", false
	}
	replaced, ok := f(inner)
	if !ok {
		return "", false
	}
	// The generated references are plain ASCII: refuse anything that
	// would need TOML escaping instead of escaping it.
	if strings.ContainsAny(replaced, `"\`) {
		return "", false
	}
	return line[:start+1] + replaced + line[end:], true
}

// verifyPatch parses both texts and proves the patch did exactly what it
// was asked to, and nothing else.
func verifyPatch(before, after, path, profile string, kind SecretKind, secret, ref string) error {
	bdoc, err := config.ParseDoc(path, []byte(before))
	if err != nil {
		return fmt.Errorf("the file does not parse: %v", err)
	}
	adoc, err := config.ParseDoc(path, []byte(after))
	if err != nil {
		return fmt.Errorf("the patched file does not parse: %v", err)
	}
	if _, ok := profileOf(bdoc, profile); !ok {
		return fmt.Errorf("profile %q is missing from %s", profile, path)
	}
	ap, ok := profileOf(adoc, profile)
	if !ok {
		return fmt.Errorf("profile %q is missing after the patch", profile)
	}
	// The reference is in, and the old literal is gone.
	var refIn, secretGone bool
	switch kind {
	case KindPassword:
		refIn = ap["password"] == ref
		secretGone = ap["password"] != secret
	case KindURL, KindODBC:
		cs, _ := config.OptConnStr(ap)
		refIn = strings.Contains(cs, ref)
		secretGone = !strings.Contains(cs, secret)
	}
	if !refIn {
		return fmt.Errorf("profile %q does not carry the reference where its %s was", profile, kind)
	}
	if !secretGone {
		return fmt.Errorf("profile %q still carries the literal secret", profile)
	}
	// Every other value is unchanged.
	for k, v := range bdoc {
		if k == "profiles" {
			continue
		}
		if !reflect.DeepEqual(v, adoc[k]) {
			return fmt.Errorf("the %s table changed outside profile %q", k, profile)
		}
	}
	for k := range adoc {
		if k == "profiles" {
			continue
		}
		if _, ok := bdoc[k]; !ok {
			return fmt.Errorf("a %s table appeared outside profile %q", k, profile)
		}
	}
	bp2, _ := bdoc["profiles"].(map[string]any)
	ap2, _ := adoc["profiles"].(map[string]any)
	for name, v := range bp2 {
		if name == profile {
			continue
		}
		if !reflect.DeepEqual(v, ap2[name]) {
			return fmt.Errorf("profile %q changed", name)
		}
	}
	return nil
}

// profileOf returns the Profile's table out of a parsed document.
func profileOf(doc map[string]any, profile string) (map[string]any, bool) {
	profiles, _ := doc["profiles"].(map[string]any)
	p, ok := profiles[profile].(map[string]any)
	return p, ok
}
