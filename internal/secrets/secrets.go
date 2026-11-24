// Package secrets resolves secret references in Profile values, so a
// config file can name where a secret lives instead of holding the secret
// itself (ADR-0008). Two reference forms are expanded in every string
// value, including inside a longer value such as a conn_str URL:
//
//   - ${env:NAME}: the value of the environment variable NAME; unset is
//     an error, so a typo fails loudly instead of sending an empty
//     password.
//   - ${cmd:command}: the standard output of command, run through the
//     shell, with trailing carriage returns and newlines trimmed. This
//     reaches every secret manager with a CLI (pass, 1Password, vault,
//     the OS keychain utilities) without hqdb knowing any of them.
//
// A string that matches neither form is literal text, so a password
// written in the file keeps working.
package secrets

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ref matches one secret reference. The body cannot contain "}", which
// keeps the form unambiguous inside braced ODBC values.
var ref = regexp.MustCompile(`\$\{(env|cmd):([^}]*)\}`)

// cmdTimeout bounds one secret command, so a command waiting on a prompt
// (an unlocked keyring, an agent asking for a passphrase) fails the call
// instead of hanging it.
const cmdTimeout = 30 * time.Second

// cacheTTL is how long a command's output is reused (see cacheEntry).
const cacheTTL = 5 * time.Minute

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
	// now is the clock, replaced in tests.
	now = time.Now
)

// cacheEntry is one command's output. A connection is opened with the
// Profile's secrets resolved, and the pool already reuses connections
// (ADR-0006), so the cache only keeps a command from running on every
// call for adapters whose connections reopen after statements (mysql,
// clickhouse native, redis); a rotated secret reaches a new connection
// within the TTL.
type cacheEntry struct {
	val string
	at  time.Time
}

// Resolve returns a copy of p with every secret reference in its string
// values expanded: strings, and the string elements of lists (the ODBC
// conn_str form). Non-string values are copied as they are, and p itself
// is never modified, so the config's raw tables stay comparable (the MCP
// pool hashes them, ADR-0006). A resolved value is not rescanned, so a
// secret containing "${" cannot trigger another lookup.
func Resolve(ctx context.Context, p map[string]any) (map[string]any, error) {
	q := make(map[string]any, len(p))
	for k, v := range p {
		switch t := v.(type) {
		case string:
			s, err := Expand(ctx, t)
			if err != nil {
				return nil, err
			}
			q[k] = s
		case []any:
			list := make([]any, len(t))
			for i, e := range t {
				s, ok := e.(string)
				if !ok {
					list[i] = e
					continue
				}
				es, err := Expand(ctx, s)
				if err != nil {
					return nil, err
				}
				list[i] = es
			}
			q[k] = list
		default:
			q[k] = v
		}
	}
	return q, nil
}

// HasReference reports whether s carries any secret reference.
func HasReference(s string) bool {
	return ref.MatchString(s)
}

// Expand expands every reference in s, returning s unchanged when it has
// none.
func Expand(ctx context.Context, s string) (string, error) {
	matches := ref.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s, nil
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		kind, body := s[m[2]:m[3]], s[m[4]:m[5]]
		b.WriteString(s[last:m[0]])
		var (
			val string
			err error
		)
		switch kind {
		case "env":
			val, err = lookupEnv(body)
		case "cmd":
			val, err = runCmd(ctx, body)
		}
		if err != nil {
			return "", err
		}
		b.WriteString(val)
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String(), nil
}

// lookupEnv reads one ${env:NAME} reference. name is part of the error
// messages, which is safe: it names the variable, never the value.
func lookupEnv(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("secret reference ${env:}: variable name is empty")
	}
	v, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("secret reference ${env:%s}: environment variable %q is not set", name, name)
	}
	return v, nil
}

// runCmd resolves one ${cmd:command} reference.
func runCmd(ctx context.Context, command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("secret reference ${cmd:}: command is empty")
	}
	if v, ok := cached(command); ok {
		return v, nil
	}
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	c := shellCmd(ctx, command)
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("secret reference ${cmd:%s}: command timed out after %s", command, cmdTimeout)
	}
	if err != nil {
		return "", fmt.Errorf("secret reference ${cmd:%s}: command failed: %v%s",
			command, err, hint(stderr.String()))
	}
	v := strings.TrimRight(stdout.String(), "\r\n")
	store(command, v)
	return v, nil
}

// hint appends the command's first stderr line to a failure, since that is
// where a secret manager says why it failed.
func hint(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	if i := strings.IndexByte(stderr, '\n'); i >= 0 {
		stderr = stderr[:i]
	}
	const max = 120
	if len(stderr) > max {
		stderr = stderr[:max] + "..."
	}
	return ": " + stderr
}

// shellCmd runs command through the system shell, so a reference can use
// quoting and pipes exactly as its user would type them.
func shellCmd(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func cached(command string) (string, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cache[command]
	if !ok || now().Sub(e.at) > cacheTTL {
		return "", false
	}
	return e.val, true
}

func store(command, val string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache[command] = cacheEntry{val: val, at: now()}
}

// Check validates a Profile's secret references without running any
// command: every ${env:NAME} must name a set variable, and every reference
// must have a non-empty body. hqdb check runs it, so a config is verified
// without touching a secret store. Values are visited in sorted key order
// so the reported error is stable.
func Check(p map[string]any) error {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch t := p[k].(type) {
		case string:
			if err := checkRefs(t); err != nil {
				return err
			}
		case []any:
			for _, e := range t {
				if s, ok := e.(string); ok {
					if err := checkRefs(s); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func checkRefs(s string) error {
	for _, m := range ref.FindAllStringSubmatch(s, -1) {
		if m[1] == "env" {
			if _, err := lookupEnv(m[2]); err != nil {
				return err
			}
		} else if m[2] == "" {
			return fmt.Errorf("secret reference ${cmd:}: command is empty")
		}
	}
	return nil
}
