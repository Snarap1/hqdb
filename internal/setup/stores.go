package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Runner runs one external command and returns its standard output:
// command is a full shell-free invocation, and stdin ("" for none) is
// written to the command's standard input, so a secret never appears in
// a command line where a CLI accepts it through stdin. Production uses
// exec; the tests fake it.
type Runner func(command string, args []string, stdin string) (string, error)

// ExecRunner is the production Runner.
func ExecRunner() Runner {
	return func(command string, args []string, stdin string) (string, error) {
		c := exec.Command(command, args...)
		if stdin != "" {
			c.Stdin = strings.NewReader(stdin)
		}
		var out strings.Builder
		c.Stdout = &out
		c.Stderr = &out
		if err := c.Run(); err != nil {
			return "", fmt.Errorf("%s failed: %v: %s", command, err, strings.TrimSpace(out.String()))
		}
		return out.String(), nil
	}
}

// Store is a secret store hqdb setup can move a credential into.
type Store struct {
	// ID is the stable identifier used in messages and tests.
	ID string
	// Title is the store as the wizard shows it.
	Title string
	// Available gates the wizard: the store can be used on this machine
	// at all, and the CLI it needs is present.
	Available bool
	// Reference returns the option value the Profile gets: the form that
	// reads the secret back where a connection is opened.
	Reference func(profile string) string
	// Move stores secret under profile and returns notes for the user:
	// wiring the store needs outside hqdb (empty for none).
	Move func(run Runner, profile, secret string) ([]string, error)
	// Verify reads the secret back and compares, proving the store and
	// the reference work before the config is touched. nil when there is
	// nothing to verify through the store (the env file).
	Verify func(run Runner, profile, secret string) error
}

// envName is the variable the env store writes for a Profile.
func envName(profile string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(profile) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return "HQDB_" + b.String() + "_PASSWORD"
}

// Stores returns every store in the order the wizard offers them: the
// platform's own first (ADR-0009), then pass, then the env file.
// envFile is where the env store writes; "" means the default beside the
// user config. The list still carries unavailable stores - a caller asks
// Available which ones the wizard can show.
func Stores(envFile string) []Store {
	return stores(runtime.GOOS, envFile)
}

// stores is Stores with the platform passed in, so every platform's
// answer is a test on every platform.
func stores(goos, envFile string) []Store {
	var list []Store
	if s, ok := osStore(goos); ok {
		list = append(list, s)
	}
	list = append(list, passStore(goos), envStore(goos, envFile))
	return list
}

// Available returns the stores the wizard can offer here.
func Available(stores []Store) []Store {
	out := make([]Store, 0, len(stores))
	for _, s := range stores {
		if s.Available {
			out = append(out, s)
		}
	}
	return out
}

// DefaultEnvFile is where the env store writes by default: env.sh beside
// the hqdb config directory.
func DefaultEnvFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hqdb", "env.sh"), nil
}

// osStore is the platform's own credential store, written through its
// CLI (ADR-0009). goos is a parameter, not runtime.GOOS, so the
// Windows answer below is a test on every platform.
func osStore(goos string) (Store, bool) {
	switch goos {
	case "windows":
		// No store on Windows. `cmdkey` writes and lists but cannot read
		// a password back, so the read would have to be hqdb's own
		// Credential Manager call - and a binary that pulls a stored
		// password out and prints it is what an infostealer looks like
		// to an antivirus, a store reviewer, and an endpoint agent.
		// Windows gets only pass, and only when its own CLI is there.
		return Store{}, false
	case "darwin":
		return Store{
			ID:        "keychain",
			Title:     "macOS Keychain (security)",
			Available: execLook("security"),
			Reference: func(profile string) string {
				return fmt.Sprintf("${cmd:security find-generic-password -s hqdb -a %s -w}", profile)
			},
			Move: func(run Runner, profile, secret string) ([]string, error) {
				if _, err := run("security", []string{
					"add-generic-password", "-s", "hqdb", "-a", profile, "-w", secret, "-U",
				}, ""); err != nil {
					return nil, err
				}
				return nil, nil
			},
			Verify: func(run Runner, profile, secret string) error {
				got, err := run("security", []string{
					"find-generic-password", "-s", "hqdb", "-a", profile, "-w",
				}, "")
				if err != nil {
					return err
				}
				return sameSecret(got, secret)
			},
		}, true
	default:
		return Store{
			ID:        "secret-service",
			Title:     "Secret Service (secret-tool)",
			Available: execLook("secret-tool"),
			Reference: func(profile string) string {
				return fmt.Sprintf("${cmd:secret-tool lookup service hqdb profile %s}", profile)
			},
			Move: func(run Runner, profile, secret string) ([]string, error) {
				if _, err := run("secret-tool", []string{
					"store", "--label=hqdb/" + profile, "service", "hqdb", "profile", profile,
				}, secret); err != nil {
					return nil, err
				}
				return nil, nil
			},
			Verify: func(run Runner, profile, secret string) error {
				got, err := run("secret-tool", []string{
					"lookup", "service", "hqdb", "profile", profile,
				}, "")
				if err != nil {
					return err
				}
				return sameSecret(got, secret)
			},
		}, true
	}
}

// passStore is pass, offered where its CLI is installed and the shell
// can run it. Windows is left out because pass is a bash script and
// refers to a ${cmd:...} there runs through cmd /C, which executes only
// a native program or a .bat/.cmd; gopass is the same store with a
// native .exe binary, and it is what a Windows user installs.
func passStore(goos string) Store {
	return Store{
		ID:        "pass",
		Title:     "pass",
		Available: goos != "windows" && execLook("pass"),
		Reference: func(profile string) string {
			return "${cmd:pass show hqdb/" + profile + "}"
		},
		Move: func(run Runner, profile, secret string) ([]string, error) {
			// insert -m reads the secret from stdin, so it never appears
			// in a command line or a process listing.
			if _, err := run("pass", []string{"insert", "-m", "-f", "hqdb/" + profile}, secret+"\n"); err != nil {
				return nil, err
			}
			return nil, nil
		},
		Verify: func(run Runner, profile, secret string) error {
			got, err := run("pass", []string{"show", "hqdb/" + profile}, "")
			if err != nil {
				return err
			}
			return sameSecret(got, secret)
		},
	}
}

// envStore writes the secret to a shell file the user wires up: hqdb
// cannot export a variable into the shell that launched it. It exists
// for the POSIX shells that source such a file, so Windows is left out:
// there is nothing on Windows to source it, and moving the secret into
// an unread plaintext file would only put it somewhere the config no
// longer points at (ADR-0009).
func envStore(goos, envFile string) Store {
	return Store{
		ID:        "env",
		Title:     "environment variable file",
		Available: goos != "windows",
		Reference: func(profile string) string {
			return "${env:" + envName(profile) + "}"
		},
		Move: func(_ Runner, profile, secret string) ([]string, error) {
			if envFile == "" {
				var err error
				envFile, err = DefaultEnvFile()
				if err != nil {
					return nil, err
				}
			}
			if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
				return nil, err
			}
			// Append, so one env.sh serves every Profile; a moved value
			// is dropped first so re-running setup updates, not doubles.
			name := envName(profile)
			var kept []string
			if data, err := os.ReadFile(envFile); err == nil {
				for _, line := range strings.Split(string(data), "\n") {
					if !strings.HasPrefix(line, "export "+name+"=") {
						kept = append(kept, line)
					}
				}
			}
			kept = append(kept, "export "+name+"="+shellQuote(secret))
			if err := os.WriteFile(envFile, []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
				return nil, err
			}
			return []string{"source " + envFile + " in the shell profile, or copy its variables into the MCP client's env map"}, nil
		},
		Verify: nil,
	}
}

// shellQuote single-quotes s for a POSIX shell, escaping the quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sameSecret compares a store's read-back with the secret, trimming the
// trailing newline CLIs print.
func sameSecret(got, secret string) error {
	if strings.TrimRight(got, "\r\n") != secret {
		return fmt.Errorf("the store did not read back the same value it was given; check the store's own state")
	}
	return nil
}

// execLook reports whether the named CLI is installed.
func execLook(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
