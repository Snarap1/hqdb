package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/config"
)

func newTestTree() catalogTree {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"alpha": {"adapter": "postgres"},
		"zeta":  {"adapter": "redis"},
	}}
	return newCatalogTree(cfg)
}

func downKey() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyDown} }
func upKey() tea.KeyPressMsg    { return tea.KeyPressMsg{Code: tea.KeyUp} }
func enterKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }
func rKey() tea.KeyPressMsg     { return tea.KeyPressMsg{Code: 'r', Text: "r"} }
func shiftDKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'D', Text: "D"}
}

// fakePool returns a connPool whose Open never fails and hands back a
// distinct fakeDB each time, with a counter of how many times Open's
// opener actually ran (vs. was served from cache).
func fakePool() (*connPool, *int) {
	opener, calls := countingOpener(nil)
	return &connPool{conns: map[poolKey]adapters.DB{}, open: opener}, calls
}

func TestVisibleListsOnlyExpandedDescendants(t *testing.T) {
	tr := newTestTree()
	if got := len(tr.visible()); got != 2 {
		t.Fatalf("got %d visible roots, want 2", got)
	}
	tr.roots[0].expanded = true
	tr.roots[0].children = []*node{{kind: kindDatabase, name: "hqdb", parent: tr.roots[0]}}
	visible := tr.visible()
	if len(visible) != 3 {
		t.Fatalf("got %d, want 3 (2 roots + 1 expanded child)", len(visible))
	}
	if visible[1].name != "hqdb" {
		t.Errorf("child not positioned right after its expanded parent: %+v", visible)
	}
}

func TestCollapsedChildrenStayHidden(t *testing.T) {
	tr := newTestTree()
	tr.roots[0].children = []*node{{kind: kindDatabase, name: "hqdb", parent: tr.roots[0]}}
	// expanded defaults to false: the child must not show up.
	if got := len(tr.visible()); got != 2 {
		t.Fatalf("got %d, want 2 (child of a collapsed node must be hidden)", got)
	}
}

func TestSelectedNodeClampsToValidRange(t *testing.T) {
	tr := newTestTree()
	tr.selected = 99
	if got := tr.selectedNode(); got == nil {
		t.Fatalf("selectedNode() = nil for an out-of-range index, want the last node")
	}
}

func TestUpDownMoveSelectionWithinVisibleList(t *testing.T) {
	tr := newTestTree()
	pool, _ := fakePool()
	tr, _ = tr.Update(downKey(), defaultKeyMap(), pool)
	if tr.selected != 1 {
		t.Errorf("selected = %d, want 1 after down", tr.selected)
	}
	tr, _ = tr.Update(downKey(), defaultKeyMap(), pool)
	if tr.selected != 1 {
		t.Errorf("selected = %d, want 1 (clamped at the end)", tr.selected)
	}
	tr, _ = tr.Update(upKey(), defaultKeyMap(), pool)
	if tr.selected != 0 {
		t.Errorf("selected = %d, want 0 after up", tr.selected)
	}
}

func TestScrollTopForKeepsSelectionInWindowAndClampsAtEdges(t *testing.T) {
	if got := scrollTopFor(0, 10, 5); got != 0 {
		t.Errorf("top row selected: got scrollTop=%d, want 0", got)
	}
	if got := scrollTopFor(9, 10, 5); got != 5 {
		t.Errorf("bottom row selected: got scrollTop=%d, want 5 (clamped)", got)
	}
	if got := scrollTopFor(3, 10, 5); got < 0 || got > 3 {
		t.Errorf("middle selection: got scrollTop=%d out of a sane range", got)
	}
	if got := scrollTopFor(2, 4, 10); got != 0 {
		t.Errorf("fewer rows than height: got scrollTop=%d, want 0", got)
	}
}

func TestActivateOnUnloadedNodeMarksLoadingAndReturnsCmd(t *testing.T) {
	tr := newTestTree()
	pool, _ := fakePool()
	// alpha sorts before zeta, so index 0 is selected by default.
	tr, cmd := tr.activate(pool)
	if cmd == nil {
		t.Fatal("want a non-nil load Cmd for an unloaded node")
	}
	if !tr.roots[0].loading {
		t.Errorf("node not marked loading")
	}
}

func TestActivateTwiceWhileLoadingDoesNotRefire(t *testing.T) {
	tr := newTestTree()
	pool, _ := fakePool()
	tr, cmd1 := tr.activate(pool)
	if cmd1 == nil {
		t.Fatal("want a Cmd on the first activate")
	}
	tr, cmd2 := tr.activate(pool)
	if cmd2 != nil {
		t.Fatal("a second activate while already loading must not fire another Cmd")
	}
}

func TestActivateCollapsesAnAlreadyExpandedLoadedNode(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.loaded = true
	root.expanded = true
	root.children = []*node{{kind: kindDatabase, name: "hqdb", parent: root}}
	pool, calls := fakePool()

	tr, cmd := tr.activate(pool)
	if cmd != nil {
		t.Fatal("collapsing must not re-fetch")
	}
	if tr.roots[0].expanded {
		t.Errorf("expanded = true, want false after collapsing")
	}
	if *calls != 0 {
		t.Errorf("opener called %d times, want 0", *calls)
	}

	// Activating again re-expands using the already-cached children.
	tr, cmd = tr.activate(pool)
	if cmd != nil {
		t.Fatal("re-expanding a loaded node must not re-fetch")
	}
	if !tr.roots[0].expanded {
		t.Errorf("expanded = false, want true after re-expanding")
	}
}

func TestActivateOnLoadMoreFetchesIntoParent(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.expanded = true
	root.loaded = true
	root.loadedN = 1
	table := &node{kind: kindDatabase, name: "hqdb", parent: root}
	root.children = []*node{table, {kind: kindLoadMore, parent: root}}
	tr.selected = 2 // the load-more row

	pool, _ := fakePool()
	tr, cmd := tr.activate(pool)
	if cmd == nil {
		t.Fatal("want a Cmd for the load-more row")
	}
	if !root.loading {
		t.Errorf("parent not marked loading")
	}
}

func TestActivateOnColumnLeafIsNoop(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.expanded = true
	root.children = []*node{{kind: kindColumn, name: "id", parent: root}}
	tr.selected = 1

	pool, _ := fakePool()
	_, cmd := tr.activate(pool)
	if cmd != nil {
		t.Fatal("a Column leaf must not produce a Cmd")
	}
}

func TestRefreshResetsChildrenAndForcesProfileReconnect(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.loaded = true
	root.expanded = true
	root.children = []*node{{kind: kindDatabase, name: "stale", parent: root}}
	pool, calls := fakePool()
	// Open once so there is a cached connection for refresh to drop.
	if _, err := pool.Open(poolKey{profile: root.name}); err != nil {
		t.Fatal(err)
	}

	tr, cmd := tr.refresh(pool)
	if cmd == nil {
		t.Fatal("want a load Cmd from refresh")
	}
	if len(root.children) != 0 || root.loaded {
		t.Errorf("refresh did not reset children/loaded: children=%+v loaded=%v", root.children, root.loaded)
	}
	if _, ok := pool.conns[poolKey{profile: root.name}]; ok {
		t.Errorf("refresh on a Profile node did not drop the cached connection")
	}
	if *calls != 1 {
		t.Errorf("opener called %d times, want 1 (the initial Open)", *calls)
	}
}

func TestRefreshIsNoopOnColumnLeaf(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.expanded = true
	root.children = []*node{{kind: kindColumn, name: "id", parent: root}}
	tr.selected = 1

	pool, _ := fakePool()
	_, cmd := tr.refresh(pool)
	if cmd != nil {
		t.Fatal("refresh on a Column leaf must be a noop")
	}
}

func TestDisconnectDropsConnectionAndCollapsesProfile(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.loaded = true
	root.expanded = true
	root.children = []*node{{kind: kindDatabase, name: "hqdb", parent: root}}
	pool, _ := fakePool()
	if _, err := pool.Open(poolKey{profile: root.name}); err != nil {
		t.Fatal(err)
	}

	tr.disconnect(pool)

	if _, ok := pool.conns[poolKey{profile: root.name}]; ok {
		t.Errorf("disconnect did not drop the connection")
	}
	if root.expanded || root.loaded || len(root.children) != 0 {
		t.Errorf("disconnect did not collapse the profile: %+v", root)
	}
}

func TestDisconnectOnNonProfileNodeIsNoop(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.expanded = true
	db := &node{kind: kindDatabase, name: "hqdb", parent: root, loaded: true}
	root.children = []*node{db}
	tr.selected = 1

	pool, _ := fakePool()
	tr.disconnect(pool)

	if !db.loaded {
		t.Errorf("disconnect acted on a non-Profile node")
	}
}

func TestUpdateWiresExpandRefreshDisconnectKeys(t *testing.T) {
	tr := newTestTree()
	pool, _ := fakePool()
	keys := defaultKeyMap()

	tr, cmd := tr.Update(enterKey(), keys, pool)
	if cmd == nil {
		t.Fatal("enter did not trigger a load Cmd")
	}
	tr.roots[0].loading = false
	tr.roots[0].loaded = true
	tr.roots[0].expanded = true

	tr, cmd = tr.Update(rKey(), keys, pool)
	if cmd == nil {
		t.Fatal("r did not trigger a refresh Cmd")
	}
	tr.roots[0].loading = false

	if _, err := pool.Open(poolKey{profile: tr.roots[0].name}); err != nil {
		t.Fatal(err)
	}
	tr, _ = tr.Update(shiftDKey(), keys, pool)
	if _, ok := pool.conns[poolKey{profile: tr.roots[0].name}]; ok {
		t.Errorf("D did not disconnect")
	}
}

func TestViewRendersIndentationAndLoadingSuffix(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.expanded = true
	root.loading = true
	root.children = []*node{{kind: kindDatabase, name: "hqdb", parent: root}}

	out := ansi.Strip(tr.View(40, 10, true))
	if !contains(out, "alpha  loading…") {
		t.Errorf("view missing loading suffix:\n%s", out)
	}
	if !contains(out, "   ▸ hqdb") {
		t.Errorf("view missing indented child:\n%s", out)
	}
}

func slashKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: '/', Text: "/"} }
func escKey() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyEscape} }
func charKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func TestSlashOpensFilterField(t *testing.T) {
	tr := newTestTree()
	pool, _ := fakePool()
	tr, cmd := tr.Update(slashKey(), defaultKeyMap(), pool)
	if !tr.filtering {
		t.Fatalf("filtering = false, want true after /")
	}
	if cmd == nil {
		t.Fatalf("want a Cmd focusing the filter field")
	}
}

func TestTypingInFilterNarrowsVisibleNodesCaseInsensitively(t *testing.T) {
	tr := newTestTree() // roots: alpha (postgres), zeta (redis)
	pool, _ := fakePool()
	tr, _ = tr.Update(slashKey(), defaultKeyMap(), pool)

	tr, _ = tr.Update(charKey('Z'), defaultKeyMap(), pool)
	rows := tr.visible()
	if len(rows) != 1 || rows[0].name != "zeta" {
		t.Fatalf("filtered rows = %+v, want only zeta", rows)
	}
}

func TestFilterQueryNarrowsDespiteNonMatchingAncestor(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0] // alpha
	root.expanded = true
	root.loaded = true
	root.children = []*node{{kind: kindDatabase, name: "hqdb", parent: root}}
	pool, _ := fakePool()

	tr, _ = tr.Update(slashKey(), defaultKeyMap(), pool)
	for _, r := range "hqdb" {
		tr, _ = tr.Update(charKey(r), defaultKeyMap(), pool)
	}
	rows := tr.visible()
	if len(rows) != 1 || rows[0].name != "hqdb" {
		t.Fatalf("rows = %+v, want only the matching descendant (ancestor 'alpha' does not match)", rows)
	}
}

func TestClearingFilterRestoresFullTree(t *testing.T) {
	tr := newTestTree()
	pool, _ := fakePool()
	tr, _ = tr.Update(slashKey(), defaultKeyMap(), pool)
	tr, _ = tr.Update(charKey('z'), defaultKeyMap(), pool)
	if len(tr.visible()) != 1 {
		t.Fatalf("filter did not narrow the tree")
	}

	tr, _ = tr.Update(tea.KeyPressMsg{Code: tea.KeyBackspace}, defaultKeyMap(), pool)
	if tr.filterQuery != "" {
		t.Fatalf("filterQuery = %q, want empty after clearing", tr.filterQuery)
	}
	if len(tr.visible()) != 2 {
		t.Fatalf("got %d visible rows after clearing, want 2 (full tree)", len(tr.visible()))
	}
}

func TestSingleLetterKeysTypedIntoFilterDoNotTriggerActions(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.loaded = true
	root.expanded = true
	root.children = []*node{{kind: kindDatabase, name: "hqdb", parent: root}}
	pool, calls := fakePool()
	if _, err := pool.Open(poolKey{profile: root.name}); err != nil {
		t.Fatal(err)
	}
	*calls = 0

	tr, _ = tr.Update(slashKey(), defaultKeyMap(), pool)
	// "r" and "D" normally refresh/disconnect the selected node; while
	// filtering they must be typed into the field instead.
	tr, _ = tr.Update(charKey('r'), defaultKeyMap(), pool)
	tr, _ = tr.Update(charKey('D'), defaultKeyMap(), pool)

	if tr.filterQuery != "rD" {
		t.Fatalf("filterQuery = %q, want %q (letters typed, not acted on)", tr.filterQuery, "rD")
	}
	if len(root.children) != 1 {
		t.Errorf("refresh fired despite filtering: children = %+v", root.children)
	}
	if _, ok := pool.conns[poolKey{profile: root.name}]; !ok {
		t.Errorf("disconnect fired despite filtering: connection was dropped")
	}
}

func TestEscLeavesFilterFieldWithoutClearingQuery(t *testing.T) {
	tr := newTestTree()
	pool, _ := fakePool()
	tr, _ = tr.Update(slashKey(), defaultKeyMap(), pool)
	tr, _ = tr.Update(charKey('z'), defaultKeyMap(), pool)

	tr, _ = tr.Update(escKey(), defaultKeyMap(), pool)
	if tr.filtering {
		t.Errorf("filtering = true, want false after Esc")
	}
	if tr.filterQuery != "z" {
		t.Errorf("filterQuery = %q, want %q (Esc must not clear it)", tr.filterQuery, "z")
	}
	if len(tr.visible()) != 1 {
		t.Errorf("tree lost its narrowing after Esc")
	}
}

func TestUpDownNavigateFilteredListWhileFiltering(t *testing.T) {
	tr := newTestTree() // roots: alpha, zeta; "l" matches only "alpha"
	pool, _ := fakePool()
	tr, _ = tr.Update(slashKey(), defaultKeyMap(), pool)
	tr, _ = tr.Update(charKey('l'), defaultKeyMap(), pool)
	rows := tr.visible()
	if len(rows) != 1 || rows[0].name != "alpha" {
		t.Fatalf("precondition: want only alpha visible, got %+v", rows)
	}

	// With a single matching row, Down must clamp rather than move past it,
	// and must not be typed into the field.
	tr, _ = tr.Update(tea.KeyPressMsg{Code: tea.KeyDown}, defaultKeyMap(), pool)
	if tr.filterQuery != "l" {
		t.Errorf("Down was typed into the filter field: query = %q", tr.filterQuery)
	}
	if tr.selected != 0 {
		t.Errorf("selected = %d, want 0 (clamped)", tr.selected)
	}
}

func TestVisibleHidesLoadMoreRowWhileFiltering(t *testing.T) {
	tr := newTestTree()
	root := tr.roots[0]
	root.expanded = true
	root.children = []*node{
		{kind: kindDatabase, name: "hqdb", parent: root},
		{kind: kindLoadMore, parent: root},
	}
	pool, _ := fakePool()
	tr, _ = tr.Update(slashKey(), defaultKeyMap(), pool)
	tr, _ = tr.Update(charKey('h'), defaultKeyMap(), pool)

	for _, n := range tr.visible() {
		if n.kind == kindLoadMore {
			t.Errorf("load-more row shown while filtering: %+v", tr.visible())
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
