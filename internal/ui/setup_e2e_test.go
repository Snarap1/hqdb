package ui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/setup"
	"github.com/Snarap1/hqdb/internal/ui"
)

// startSetup starts the setup wizard over the found secrets, offering
// only the env store so the flow is deterministic and never touches a
// real secret manager (its Move just writes the env file).
func startSetup(t *testing.T, cfg *config.Config, envFile string) *teatest.TestModel {
	t.Helper()
	found, err := setup.Find(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var stores []setup.Store
	for _, s := range setup.Stores(envFile) {
		if s.ID == "env" {
			s.Available = true
			stores = []setup.Store{s}
		}
	}
	tm := teatest.NewTestModel(t, ui.NewSetupModel(found, stores, nil), teatest.WithInitialTermSize(100, 30))
	t.Cleanup(func() {
		tm.Quit()
		time.Sleep(50 * time.Millisecond)
	})
	return tm
}

func TestSetupWizardMovesLiteralPasswords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfgPath := filepath.Join(home, "hqdb.toml")
	text := `[profiles.prod]
adapter = "postgres"
password = "literal"

[profiles.secured]
adapter = "postgres"
password = "${cmd:pass show hqdb/secured}"
`
	if err := os.WriteFile(cfgPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HQDB_CONFIG", cfgPath)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), "env.sh")
	tm := startSetup(t, cfg, envFile)

	// The list names the one literal credential without rendering it:
	// the secret is lowercase "literal", while the hint says "Literal".
	screen := waitForText(t, tm, "prod")
	if strings.Contains(screen, "literal") {
		t.Fatalf("the wizard rendered the secret:\n%s", screen)
	}
	if strings.Contains(screen, "secured") {
		t.Errorf("the already-referenced profile was listed; it must not be:\n%s", screen)
	}
	// Enter picks the secrets, Enter the env store, Enter confirms.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForText(t, tm, "environment variable file")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForText(t, tm, "${env:HQDB_PROD_PASSWORD}")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForText(t, tm, "Run hqdb check to confirm")

	// The config carries the reference where the literal was, and the
	// env file carries the value; the referenced profile is untouched.
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `password = "${env:HQDB_PROD_PASSWORD}"`) {
		t.Errorf("the config was not patched:\n%s", got)
	}
	if strings.Contains(string(got), `password = "literal"`) {
		t.Errorf("the literal survived:\n%s", got)
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "HQDB_PROD_PASSWORD='literal'") {
		t.Errorf("the env file misses the value:\n%s", env)
	}
	if strings.Contains(string(env), "HQDB_SECURED") {
		t.Errorf("the already-referenced profile was moved anyway:\n%s", env)
	}
	// The done screen reports the move; any key quits.
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
