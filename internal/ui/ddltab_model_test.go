package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/ddl"
)

// newPostgresTableModel builds a Model with a postgres Table node selected
// (pg-dev -> hqdb -> public -> widgets), so a test can exercise "d" without
// touching a real connection.
func newPostgresTableModel() (Model, *node) {
	cfg := &config.Config{Profiles: map[string]map[string]any{"pg-dev": {"adapter": "postgres"}}}
	m := New(cfg)
	root := m.tree.roots[0]
	root.loaded, root.expanded = true, true

	table := &node{kind: kindTable, name: "widgets", objKind: "table", profile: "pg-dev", adapter: "postgres",
		path: []string{"hqdb", "public", "widgets"}, parent: root, loaded: true}
	root.children = []*node{table}

	m.tree.selected = 1
	m.width, m.height = 100, 30
	return m, table
}

func TestDKeyOnSQLTableOpensADDLTab(t *testing.T) {
	m, table := newPostgresTableModel()

	newM, cmd := m.handleKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	mm := newM.(Model)
	if cmd == nil {
		t.Fatal("want a non-nil Cmd")
	}
	if len(mm.tabs) != 1 {
		t.Fatalf("got %d tabs, want 1", len(mm.tabs))
	}
	dt, ok := mm.tabs[0].(*ddlTab)
	if !ok {
		t.Fatalf("tab is %T, want *ddlTab", mm.tabs[0])
	}
	wantTitle := dataViewTitle(table.profile, table.path) + " (DDL)"
	if dt.title != wantTitle {
		t.Errorf("tab title = %q, want %q", dt.title, wantTitle)
	}
	if !dt.loading {
		t.Errorf("tab should start loading")
	}
}

func TestDKeyDoesNothingOnMongoCollectionViaIsDDLTable(t *testing.T) {
	m, _ := newMongoCollectionModel()

	newM, cmd := m.handleKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	mm := newM.(Model)
	if cmd != nil {
		t.Fatal("'d' must do nothing on a mongodb node")
	}
	if len(mm.tabs) != 0 {
		t.Fatalf("got %d tabs, want 0", len(mm.tabs))
	}
}

func TestCopyActiveTabEmitsOSC52ForTheFullDDLDefinition(t *testing.T) {
	m, _ := newPostgresTableModel()
	m.tabs = []tab{&ddlTab{title: "pg-dev: hqdb.public.widgets (DDL)",
		ddl: &ddl.Out{Definition: "CREATE TABLE widgets ();"}}}
	m.activeTab = 0
	m.focus = paneMain

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("want a non-nil Cmd")
	}
	msg := cmd()
	if got := fmt.Sprintf("%T", msg); !strings.Contains(got, "ClipboardMsg") {
		t.Errorf("Cmd produced %T, want a clipboard-setting message", msg)
	}
	if got := fmt.Sprintf("%v", msg); got != "CREATE TABLE widgets ();" {
		t.Errorf("clipboard content = %q, want the full DDL definition", got)
	}
}

func TestCopyActiveTabNoopWhenActiveTabIsNotYetLoaded(t *testing.T) {
	m, _ := newPostgresTableModel()
	m.tabs = []tab{&ddlTab{title: "pg-dev: hqdb.public.widgets (DDL)", loading: true}}
	m.activeTab = 0
	m.focus = paneMain

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd != nil {
		t.Fatal("copy must be a no-op before the DDL has loaded")
	}
}

func TestDKeyAddsADDLTabAlongsideOpenTabsAndActivatesIt(t *testing.T) {
	m, _ := newPostgresTableModel()
	existing := &ddlTab{title: "other"}
	m.tabs = []tab{existing}
	m.activeTab = 0

	newM, _ := m.handleKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	mm := newM.(Model)
	if len(mm.tabs) != 2 || mm.tabs[0] != existing {
		t.Fatalf("got tabs %v, want the existing tab kept plus a new one", mm.tabs)
	}
	if mm.activeTab != 1 {
		t.Errorf("activeTab = %d, want 1 (the new DDL tab)", mm.activeTab)
	}
}
