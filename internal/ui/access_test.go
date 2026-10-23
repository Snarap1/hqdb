package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Snarap1/hqdb/internal/config"
)

func TestConsoleAccess(t *testing.T) {
	cases := []struct {
		name      string
		access    config.Access
		violation string
		want      string // "run", "ask", or "refuse"
	}{
		{"a read runs on a read-only profile", config.ReadOnly, "", "run"},
		{"a write is refused on a read-only profile", config.ReadOnly, "DELETE is not allowed", "refuse"},
		{"a write asks on a read-write profile", config.ReadWrite, "DELETE is not allowed", "ask"},
		{"a read runs on a read-write profile", config.ReadWrite, "", "run"},
		{"the zero access is read-only", "", "DELETE is not allowed", "refuse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var core consoleCore
			core.profile = "p"
			core.setAccess(c.access)
			ran := false
			core.runOrAsk("stmt", c.violation, func() tea.Cmd { ran = true; return nil })
			got := "run"
			switch {
			case core.confirm.pending():
				got = "ask"
			case core.err != nil:
				got = "refuse"
			}
			if got != c.want || (got == "run") != ran {
				t.Fatalf("got %s (ran %v), want %s", got, ran, c.want)
			}
			if got == "refuse" && core.err.Error() != "Refused (read-only profile): "+c.violation {
				t.Errorf("refusal = %q", core.err)
			}
		})
	}
}

func TestReadOnlyConsoleRefusesAWriteInItsView(t *testing.T) {
	tb := newConsoleTab("pg-dev", "", nil)
	tb.editor = editor{}.insert("DROP TABLE t")
	tb.runCmd(nil)
	v := ansi.Strip(tb.View(120, 30, true))
	if !strings.Contains(v, "Refused (read-only profile)") || strings.Contains(v, "Run? y/N") {
		t.Errorf("view must show the refusal, not a confirmation:\n%s", v)
	}
}

func TestConsoleTakesItsProfilesAccess(t *testing.T) {
	for _, access := range []config.Access{config.ReadOnly, config.ReadWrite} {
		cfg := &config.Config{Profiles: map[string]map[string]any{
			"pg-dev": {"adapter": "postgres", "access": string(access)},
		}}
		m := New(cfg)
		m.width, m.height = 100, 30
		newM, _ := m.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
		ct := newM.(Model).tabs[0].(*consoleTab)
		if ct.access != access {
			t.Errorf("Console access = %q, want %q", ct.access, access)
		}
	}
}

func TestReadOnlyDataViewRefusesAWritingWhere(t *testing.T) {
	cases := []struct {
		name    string
		access  config.Access
		where   string
		orderBy string
		refused bool
	}{
		{"a stacked delete in WHERE", config.ReadOnly, "1=1; DELETE FROM widgets", "", true},
		{"a write keyword in ORDER BY", config.ReadOnly, "", "id; DROP TABLE widgets", true},
		{"a plain filter", config.ReadOnly, "price < 2 AND name <> 'drop'", "id DESC", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newDataViewTab("pg-dev", "postgres", []string{"hqdb", "public", "widgets"})
			d.setAccess(c.access)
			d.where, d.orderBy = c.where, c.orderBy
			err := d.screen()
			if (err != nil) != c.refused {
				t.Fatalf("screen() = %v, refused want %v", err, c.refused)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "Refused (read-only profile): ") {
				t.Errorf("refusal = %q", err)
			}
		})
	}
}

func TestReadWriteDataViewRunsAnyWhere(t *testing.T) {
	d := newDataViewTab("pg-dev", "postgres", []string{"hqdb", "public", "widgets"})
	d.setAccess(config.ReadWrite)
	d.where = "1=1; DELETE FROM widgets"
	if err := d.screen(); err != nil {
		t.Errorf("screen() = %v on a read-write Profile", err)
	}
}

func TestReadOnlyDataViewLoadNeverReachesTheDatabase(t *testing.T) {
	d := newDataViewTab("pg-dev", "postgres", []string{"hqdb", "public", "widgets"})
	d.where = "1=1; DELETE FROM widgets"
	// A nil pool panics if the load tries to connect.
	msg := d.loadCmd(nil)().(pageViewMsg)
	if msg.err == nil || !strings.HasPrefix(msg.err.Error(), "Refused (read-only profile): ") {
		t.Errorf("load err = %v, want the refusal", msg.err)
	}
	cmsg := d.countCmd(nil)().(countViewMsg)
	if cmsg.err == nil {
		t.Error("count must be refused too")
	}
	if d.exportCmd(nil, filepath.Join(t.TempDir(), "x.csv")) != nil || !strings.Contains(d.exportStatus, "Refused (read-only profile)") {
		t.Errorf("export status = %q, want the refusal", d.exportStatus)
	}
}

func TestDataViewTakesItsProfilesAccess(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"pg-dev": {"adapter": "postgres", "access": "read-write"},
	}}
	m := New(cfg)
	n := &node{kind: kindTable, profile: "pg-dev", adapter: "postgres", path: []string{"hqdb", "public", "widgets"}}
	m, _ = m.openDataView(n)
	if d := m.tabs[0].(*dataViewTab); d.access != config.ReadWrite {
		t.Errorf("Data view access = %q, want read-write", d.access)
	}
}
