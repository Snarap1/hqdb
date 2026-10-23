package ui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/catalog"
)

func TestApplyCatalogMsgReplacesChildrenOnFreshSchemaLoad(t *testing.T) {
	root := &node{kind: kindProfile, name: "pg-dev", adapter: "postgres"}
	db := &node{kind: kindDatabase, name: "hqdb", profile: "pg-dev", adapter: "postgres", path: []string{"hqdb"}, parent: root}

	applyCatalogMsg(catalogMsg{
		node: db,
		schema: &catalog.SchemaOut{
			Nodes: []catalog.SchemaNode{{Name: "public", Kind: "schema"}},
		},
	})

	if !db.loaded || !db.expanded || db.loading {
		t.Fatalf("db state = loaded=%v expanded=%v loading=%v", db.loaded, db.expanded, db.loading)
	}
	if len(db.children) != 1 || db.children[0].name != "public" || db.children[0].kind != kindSchema {
		t.Fatalf("children = %+v", db.children)
	}
	if db.children[0].parent != db {
		t.Errorf("child.parent not set")
	}
}

func TestApplyCatalogMsgAddsLoadMoreWhenTruncated(t *testing.T) {
	root := &node{kind: kindProfile, name: "my-dev", adapter: "mysql"}

	applyCatalogMsg(catalogMsg{
		node: root,
		schema: &catalog.SchemaOut{
			Nodes:     []catalog.SchemaNode{{Name: "hqdb", Kind: "database"}},
			Truncated: true,
		},
	})

	if len(root.children) != 2 {
		t.Fatalf("got %d children, want 2 (1 real + load-more)", len(root.children))
	}
	if root.children[1].kind != kindLoadMore {
		t.Errorf("last child kind = %v, want kindLoadMore", root.children[1].kind)
	}
	if root.loadedN != 1 {
		t.Errorf("loadedN = %d, want 1 (load-more sentinel excluded)", root.loadedN)
	}
}

func TestApplyCatalogMsgLoadMoreAppendsPastSentinel(t *testing.T) {
	parent := &node{kind: kindDatabase, name: "hqdb", adapter: "mysql"}
	parent.children = []*node{
		{kind: kindTable, name: "t1", parent: parent},
		{kind: kindLoadMore, parent: parent},
	}
	parent.loadedN = 1

	applyCatalogMsg(catalogMsg{
		node:     parent,
		loadMore: true,
		schema: &catalog.SchemaOut{
			Nodes: []catalog.SchemaNode{{Name: "t2", Kind: "table"}},
		},
	})

	if len(parent.children) != 2 {
		t.Fatalf("got %d children, want 2 (t1, t2, no more sentinel)", len(parent.children))
	}
	if parent.children[0].name != "t1" || parent.children[1].name != "t2" {
		t.Fatalf("children = %+v", parent.children)
	}
	if parent.loadedN != 2 {
		t.Errorf("loadedN = %d, want 2", parent.loadedN)
	}
}

func TestApplyCatalogMsgColumnsBuildsLeaves(t *testing.T) {
	table := &node{kind: kindTable, name: "widgets", profile: "pg-dev", adapter: "postgres", path: []string{"hqdb", "public", "widgets"}}

	applyCatalogMsg(catalogMsg{
		node: table,
		columns: &catalog.ColumnsOut{
			Columns: []catalog.Column{{Name: "id", Type: "integer", Nullable: "NO"}},
		},
	})

	if len(table.children) != 1 || table.children[0].kind != kindColumn {
		t.Fatalf("children = %+v", table.children)
	}
	if !table.loaded || !table.expanded {
		t.Errorf("table not marked loaded/expanded")
	}
}

func TestApplyCatalogMsgConnectErrorMarksProfileRootBroken(t *testing.T) {
	root := &node{kind: kindProfile, name: "dead-dev", adapter: "postgres"}
	db := &node{kind: kindDatabase, name: "hqdb", parent: root}
	wantErr := errors.New("dial tcp: connection refused")

	applyCatalogMsg(catalogMsg{node: db, err: wantErr, connectErr: true})

	if root.connectErr != wantErr {
		t.Errorf("root.connectErr = %v, want %v", root.connectErr, wantErr)
	}
	if db.fetchErr != wantErr {
		t.Errorf("db.fetchErr = %v, want %v (so selecting it also explains why)", db.fetchErr, wantErr)
	}
	if db.loading {
		t.Errorf("db.loading still true after the error arrived")
	}
}

func TestApplyCatalogMsgFetchErrorSetsNodeOnly(t *testing.T) {
	root := &node{kind: kindProfile, name: "pg-dev", adapter: "postgres"}
	db := &node{kind: kindDatabase, name: "hqdb", parent: root}
	wantErr := errors.New("permission denied for schema public")

	applyCatalogMsg(catalogMsg{node: db, err: wantErr})

	if db.fetchErr != wantErr {
		t.Errorf("db.fetchErr = %v, want %v", db.fetchErr, wantErr)
	}
	if root.connectErr != nil {
		t.Errorf("a plain fetch error must not mark the profile root broken")
	}
}

func TestApplyCatalogMsgSuccessClearsPriorConnectError(t *testing.T) {
	root := &node{kind: kindProfile, name: "pg-dev", adapter: "postgres", connectErr: errors.New("stale")}

	applyCatalogMsg(catalogMsg{node: root, schema: &catalog.SchemaOut{
		Nodes: []catalog.SchemaNode{{Name: "hqdb", Kind: "database"}},
	}})

	if root.connectErr != nil {
		t.Errorf("connectErr = %v, want nil after a successful load", root.connectErr)
	}
}

// fakeLoadDB fails get_schema/get_columns deterministically: catalog.GetSchema
// type-switches on the concrete adapter connection types and falls through
// to its "not available" error for anything else, so a fake DB lets us
// exercise loadCmd's connect-vs-fetch error wiring without any network.
type fakeLoadDB struct{}

func (fakeLoadDB) Kind() string { return "fake" }
func (fakeLoadDB) Close()       {}

func TestLoadCmdReportsConnectErrorWhenOpenFails(t *testing.T) {
	wantErr := errors.New("dial tcp: refused")
	pool := &connPool{conns: map[string]adapters.DB{}, open: func(string) (adapters.DB, error) {
		return nil, wantErr
	}}
	root := &node{kind: kindProfile, name: "dead-dev", adapter: "postgres"}

	msg := loadCmd(&cancelSet{}, pool, root, false)().(catalogMsg)
	if !msg.connectErr || !errors.Is(msg.err, wantErr) {
		t.Fatalf("got %+v, want a connectErr wrapping %v", msg, wantErr)
	}
}

func TestLoadCmdRetriesOnceThenReportsConnectErrorWhenTheQueryKeepsFailing(t *testing.T) {
	// database/sql connects lazily on first use, so Open rarely fails by
	// itself; the query is where a dead server actually shows up. A fake
	// DB's query fails deterministically (catalog.GetSchema has no case
	// for it), so after the one Reopen retry also fails the same way,
	// loadCmd must report the Profile itself as unreachable rather than
	// leaving a healthy-looking Profile with a buried per-node error.
	opens := 0
	pool := &connPool{conns: map[string]adapters.DB{}, open: func(string) (adapters.DB, error) {
		opens++
		return fakeLoadDB{}, nil
	}}
	root := &node{kind: kindProfile, name: "fake-dev", adapter: "postgres"}

	msg := loadCmd(&cancelSet{}, pool, root, false)().(catalogMsg)
	if !msg.connectErr {
		t.Fatalf("got connectErr=false, want true once the retry also failed: %+v", msg)
	}
	if msg.err == nil {
		t.Fatalf("want a non-nil error")
	}
	if opens != 2 {
		t.Errorf("opener called %d times, want 2 (the initial attempt and the one retry)", opens)
	}
}

func TestApplyFilterSearchMsgAddsHitsDedupedAgainstLoadedChildren(t *testing.T) {
	root := &node{kind: kindProfile, name: "my-dev", adapter: "mysql"}
	db := &node{kind: kindDatabase, name: "hqdb", profile: "my-dev", adapter: "mysql", path: []string{"hqdb"}, parent: root}
	db.children = []*node{
		{kind: kindTable, name: "widgets", parent: db},
		{kind: kindLoadMore, parent: db},
	}
	tr := catalogTree{filterQuery: "wid"}

	tr = applyFilterSearchMsg(tr, filterSearchMsg{
		node:  db,
		query: "wid",
		nodes: []catalog.SchemaNode{
			{Name: "widgets", Kind: "table"},  // already loaded: must not duplicate
			{Name: "widgets2", Kind: "table"}, // new: beyond the loaded chunk
		},
	})

	if len(db.searchHits) != 1 || db.searchHits[0].name != "widgets2" {
		t.Fatalf("searchHits = %+v, want only the one new match", db.searchHits)
	}
	if db.searchHits[0].kind != kindTable || db.searchHits[0].parent != db {
		t.Errorf("search hit not built as a proper child: %+v", db.searchHits[0])
	}
}

func TestApplyFilterSearchMsgDiscardsStaleQuery(t *testing.T) {
	db := &node{kind: kindDatabase, name: "hqdb"}
	tr := catalogTree{filterQuery: "newer"} // the user kept typing since the request fired

	tr = applyFilterSearchMsg(tr, filterSearchMsg{
		node:  db,
		query: "older",
		nodes: []catalog.SchemaNode{{Name: "stale-match", Kind: "table"}},
	})

	if db.searchHits != nil {
		t.Errorf("searchHits = %+v, want nil: a stale response must be discarded", db.searchHits)
	}
}

func TestApplyFilterSearchMsgDiscardsOnError(t *testing.T) {
	db := &node{kind: kindDatabase, name: "hqdb"}
	tr := catalogTree{filterQuery: "wid"}

	tr = applyFilterSearchMsg(tr, filterSearchMsg{node: db, query: "wid", err: errors.New("boom")})

	if db.searchHits != nil {
		t.Errorf("searchHits = %+v, want nil on error", db.searchHits)
	}
	if db.fetchErr != nil {
		t.Errorf("fetchErr = %v, want nil: a failed search must not mark the node broken", db.fetchErr)
	}
}

func TestSearchCmdReturnsErrWhenOpenFails(t *testing.T) {
	wantErr := errors.New("dial tcp: refused")
	pool := &connPool{conns: map[string]adapters.DB{}, open: func(string) (adapters.DB, error) {
		return nil, wantErr
	}}
	root := &node{kind: kindProfile, name: "dead-dev", adapter: "postgres"}
	db := &node{kind: kindDatabase, name: "hqdb", parent: root, path: []string{"hqdb"}}

	msg := searchCmd(&cancelSet{}, pool, db, "wid")().(filterSearchMsg)
	if !errors.Is(msg.err, wantErr) || msg.query != "wid" || msg.node != db {
		t.Fatalf("got %+v, want err wrapping %v for query %q", msg, wantErr, "wid")
	}
}

var _ tea.Msg = catalogMsg{}
var _ tea.Msg = filterSearchMsg{}
