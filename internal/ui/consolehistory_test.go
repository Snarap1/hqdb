package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/config"
)

// A state directory that cannot be created (here: under a regular file)
// must not stop a Console from opening or accepting text.
func TestConsoleOpensWithUnusableStateDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Profiles: map[string]map[string]any{"pg-dev": {"adapter": "postgres"}}}
	m := New(cfg, WithStateDir(filepath.Join(file, "hq")))
	m.width, m.height = 100, 30
	newM, _ := m.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	mm := newM.(Model)
	if len(mm.tabs) != 1 {
		t.Fatalf("got %d tabs, want the Console", len(mm.tabs))
	}
	newM, _ = mm.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	ct := newM.(Model).tabs[0].(*consoleTab)
	if got := ct.editor.text(); got != "x" {
		t.Fatalf("editor = %q, want the typed text", got)
	}
}

// Recall walks older and newer through the history and gives back the
// unsaved draft when stepping past the newest entry.
func TestConsoleHistoryRecallRestoresDraft(t *testing.T) {
	keys := defaultKeyMap()
	ct := newConsoleTab("pg", "", nil)
	ct.hist.record("SELECT 1")
	ct.hist.record("SELECT 2")
	ct.editor = ct.editor.insert("draft")
	up := tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl}
	down := tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl}
	for _, step := range []struct {
		k    tea.KeyPressMsg
		want string
	}{{up, "SELECT 2"}, {up, "SELECT 1"}, {up, "SELECT 1"}, {down, "SELECT 2"}, {down, "draft"}, {down, "draft"}} {
		ct.Update(step.k, keys, nil)
		if got := ct.editor.text(); got != step.want {
			t.Fatalf("editor = %q, want %q", got, step.want)
		}
	}
}
