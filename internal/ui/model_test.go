package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Snarap1/hqdb/internal/config"
)

// newTestModel builds a Model around the same two-Profile fixture
// tree_test.go uses, with a fake connPool so no test here ever touches a
// driver or network.
func newTestModel() Model {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"alpha": {"adapter": "postgres"},
		"zeta":  {"adapter": "redis"},
	}}
	pool, _ := fakePool()
	return Model{cfg: cfg, keys: defaultKeyMap(), pool: pool, tree: newCatalogTree(cfg), focus: paneTree}
}

func TestGlobalSingleLetterKeysDoNotFireWhileTreeFilterIsFocused(t *testing.T) {
	m := newTestModel()
	tm, _ := m.handleKey(slashKey())
	m = tm.(Model)
	if !m.tree.filtering {
		t.Fatalf("precondition: / did not open the filter field")
	}

	// q would normally quit (returning tea.Quit without ever reaching the
	// tree); z would normally hide the tree; ? would normally open the
	// help overlay. While the filter field has focus none of that must
	// happen - the letters belong to the query, which is checked below by
	// asserting every one of them landed in filterQuery: had "q" taken the
	// Quit branch instead, it would never have reached tree.Update and so
	// would be missing from the accumulated query.
	for _, r := range []rune{'q', 'z', '?'} {
		tm, _ = m.handleKey(charKey(r))
		m = tm.(Model)
	}

	if m.treeHidden {
		t.Errorf("tree hidden: 'z' acted on the global binding instead of being typed")
	}
	if m.showHelp {
		t.Errorf("help shown: '?' acted on the global binding instead of being typed")
	}
	if m.tree.filterQuery != "qz?" {
		t.Errorf("filterQuery = %q, want %q (every key typed into the field)", m.tree.filterQuery, "qz?")
	}
}

func TestEscClosesHelpWhenTreeIsNotFiltering(t *testing.T) {
	m := newTestModel()
	m.showHelp = true
	tm, _ := m.handleKey(escKey())
	m = tm.(Model)
	if m.showHelp {
		t.Errorf("showHelp still true after Esc")
	}
}

// newMongoCollectionModel builds a Model whose tree is already expanded
// down to a mongodb Collection node (mongo-dev -> hqdb -> widgets), with
// that Collection selected, so a test can exercise what Enter or a
// double-click does to it without touching a real connection.
func newMongoCollectionModel() (Model, *node) {
	cfg := &config.Config{Profiles: map[string]map[string]any{"mongo-dev": {"adapter": "mongodb"}}}
	m := New(cfg)
	root := m.tree.roots[0]
	root.loaded, root.expanded = true, true

	db := &node{kind: kindDatabase, name: "hqdb", profile: "mongo-dev", adapter: "mongodb",
		path: []string{"hqdb"}, parent: root, loaded: true, expanded: true}
	root.children = []*node{db}

	coll := &node{kind: kindTable, name: "widgets", objKind: "collection", profile: "mongo-dev", adapter: "mongodb",
		path: []string{"hqdb", "widgets"}, parent: db, loaded: true}
	db.children = []*node{coll}

	m.tree.selected = 2 // root(0), db(1), coll(2)
	m.width, m.height = 100, 30
	return m, coll
}

func TestEnterOnMongoCollectionOpensDataViewTab(t *testing.T) {
	m, coll := newMongoCollectionModel()

	newM, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm := newM.(Model)
	if cmd == nil {
		t.Fatal("want a non-nil Cmd")
	}
	if len(mm.tabs) != 1 {
		t.Fatalf("got %d tabs, want 1", len(mm.tabs))
	}
	mt, ok := mm.tabs[0].(*mongoTab)
	if !ok {
		t.Fatalf("tab = %T, want *mongoTab", mm.tabs[0])
	}
	if want := dataViewTitle(coll.profile, coll.path); mt.Title() != want {
		t.Errorf("tab title = %q, want %q", mt.Title(), want)
	}
	if !mt.loading {
		t.Errorf("tab should start loading")
	}
}

func TestEnterOnSQLTableOpensDataViewTab(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{"pg-dev": {"adapter": "postgres"}}}
	m := New(cfg)
	root := m.tree.roots[0]
	root.loaded, root.expanded = true, true
	table := &node{kind: kindTable, name: "widgets", objKind: "table", profile: "pg-dev", adapter: "postgres",
		path: []string{"hqdb", "public", "widgets"}, parent: root, loaded: true}
	root.children = []*node{table}
	m.tree.selected = 1
	m.width, m.height = 100, 30

	newM, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm := newM.(Model)
	if cmd == nil {
		t.Fatal("want a non-nil Cmd for the SQL Data view's first page load")
	}
	if len(mm.tabs) != 1 {
		t.Fatalf("got %d tabs, want 1 (a SQL Table also opens a Data view, like mongodb)", len(mm.tabs))
	}
	dv, ok := mm.tabs[0].(*dataViewTab)
	if !ok {
		t.Fatalf("tab = %T, want *dataViewTab", mm.tabs[0])
	}
	if want := dataViewTitle(table.profile, table.path); dv.Title() != want {
		t.Errorf("tab title = %q, want %q", dv.Title(), want)
	}
	if mm.focus != paneMain {
		t.Errorf("focus = %v, want paneMain after opening a Data view", mm.focus)
	}
}

func TestEnterOnRedisKeyPrefixDoesNotOpenATab(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{"redis-dev": {"adapter": "redis"}}}
	m := New(cfg)
	root := m.tree.roots[0]
	root.loaded, root.expanded = true, true
	key := &node{kind: kindKeyPrefix, name: "user", objKind: "namespace", profile: "redis-dev", adapter: "redis",
		path: []string{"user"}, parent: root, loaded: true}
	root.children = []*node{key}
	m.tree.selected = 1
	m.width, m.height = 100, 30

	newM, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm := newM.(Model)
	if len(mm.tabs) != 0 {
		t.Fatalf("got %d tabs, want 0 (only a key opens a value tab)", len(mm.tabs))
	}
}

func TestDKeyDoesNothingOnMongoCollection(t *testing.T) {
	m, _ := newMongoCollectionModel()

	newM, cmd := m.handleKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	mm := newM.(Model)
	if cmd != nil {
		t.Fatal("'d' is not wired to anything yet; must stay a no-op on mongodb nodes")
	}
	if len(mm.tabs) != 0 {
		t.Fatalf("got %d tabs, want 0", len(mm.tabs))
	}
}

func TestDoubleClickOnMongoCollectionOpensDataViewTab(t *testing.T) {
	m, coll := newMongoCollectionModel()
	// Row 2 (root, db, coll) renders at screen row 3, below the tree
	// pane's top border, with nothing scrolled.
	newM, _ := m.handleClick(tea.Mouse{X: 5, Y: 3, Button: tea.MouseLeft})
	m = newM.(Model)
	newM, cmd := m.handleClick(tea.Mouse{X: 5, Y: 3, Button: tea.MouseLeft})
	mm := newM.(Model)
	if cmd == nil {
		t.Fatal("want a non-nil Cmd from the second (double) click")
	}
	if len(mm.tabs) != 1 || mm.tabs[0].Title() != dataViewTitle(coll.profile, coll.path) {
		t.Fatalf("got tabs=%+v, want one Data view tab for %s", mm.tabs, coll.name)
	}
}

func TestMainContentRendersActiveTabOverNodeDetails(t *testing.T) {
	m, _ := newMongoCollectionModel()
	mt := newMongoTab("mongo-dev", []string{"hqdb", "widgets"})
	mt.loading = true
	m.tabs = []tab{mt}
	got := ansi.Strip(m.mainContent(100, 30))
	for _, want := range []string{"DATA mongo-dev hqdb.widgets", "loading..."} {
		if !contains(got, want) {
			t.Errorf("mainContent() = %q, want it to contain %q", got, want)
		}
	}
}

func TestOpenDataViewAppendsRatherThanReplacingAnExistingTab(t *testing.T) {
	m, coll := newMongoCollectionModel()
	m, cmd1 := m.openDataView(coll)
	if cmd1 == nil || len(m.tabs) != 1 {
		t.Fatalf("first open: got %d tabs, want 1", len(m.tabs))
	}
	m, cmd2 := m.openDataView(coll)
	if cmd2 == nil || len(m.tabs) != 2 {
		t.Fatalf("second open: got %d tabs, want 2 (opening again adds another tab)", len(m.tabs))
	}
	if m.activeTab != 1 {
		t.Errorf("activeTab = %d, want 1 (the newest tab)", m.activeTab)
	}
}

func TestTabManagementCyclesAndCloses(t *testing.T) {
	m, coll := newMongoCollectionModel()
	m, _ = m.openDataView(coll)
	m, _ = m.openDataView(coll)
	if len(m.tabs) != 2 {
		t.Fatalf("got %d tabs, want 2", len(m.tabs))
	}

	m = m.prevTab()
	if m.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 after prevTab from 1", m.activeTab)
	}
	m = m.prevTab()
	if m.activeTab != 1 {
		t.Errorf("activeTab = %d, want 1 (wraps around)", m.activeTab)
	}
	m = m.nextTab()
	if m.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (wraps the other way)", m.activeTab)
	}

	m = m.closeActiveTab()
	if len(m.tabs) != 1 {
		t.Fatalf("got %d tabs after close, want 1", len(m.tabs))
	}
	if m.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 after closing", m.activeTab)
	}

	m = m.closeActiveTab()
	if len(m.tabs) != 0 {
		t.Fatalf("got %d tabs after closing the last one, want 0", len(m.tabs))
	}
	// Closing with no tabs left must not panic.
	m = m.closeActiveTab()
	if len(m.tabs) != 0 {
		t.Fatalf("got %d tabs, want 0", len(m.tabs))
	}
}

func TestOpenCellPopupShowsCellModeAndCopiesItsValue(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 30
	dv := newDataViewTab("alpha", "postgres", []string{"hqdb", "public", "widgets"})
	dv.grid = newGrid([]string{"name"}, [][]any{{"bolt"}})
	m.tabs, m.focus = []tab{dv}, paneMain

	newM, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = newM.(Model)
	if got := m.mode(); got != "CELL" {
		t.Fatalf("mode with the cell popup open = %q, want CELL", got)
	}
	if bar := ansi.Strip(renderStatusBar(100, m.mode(), m.paneHelp(), "")); !contains(bar, "y copy") || !contains(bar, "esc close") {
		t.Errorf("status bar with the popup open = %q, want the popup's own keys", bar)
	}
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"}); cmd == nil {
		t.Error("y in the open popup must copy the cell")
	}
}
