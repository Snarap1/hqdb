package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Snarap1/hqdb/internal/catalog"
)

func TestRedisLevelChildrenSplitsPrefixesKeysAndBoth(t *testing.T) {
	root := &node{kind: kindProfile, name: "r", profile: "r", adapter: "redis"}
	got := levelChildren(root, []catalog.SchemaNode{
		{Name: "plain", Kind: "key", Count: 1},
		{Name: "queue", Kind: "namespace", Count: 3},
		{Name: "user", Kind: "key", Count: 2}, // "user" and "user:1"
	})
	var labels []string
	for _, c := range got {
		labels = append(labels, labelText(c))
	}
	want := []string{"plain [key]", "queue [prefix]", "user [prefix]", "user [key]"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	if got[3].kind.expandable() || !got[2].kind.expandable() {
		t.Errorf("a key must be a leaf and a prefix expandable")
	}
}

func TestNextKindPostgresHasSchemas(t *testing.T) {
	if got := nextKind("postgres", kindDatabase); got != kindSchema {
		t.Errorf("postgres database -> %v, want kindSchema", got)
	}
	if got := nextKind("mssql", kindDatabase); got != kindSchema {
		t.Errorf("mssql database -> %v, want kindSchema", got)
	}
	if got := nextKind("odbc", kindDatabase); got != kindSchema {
		t.Errorf("odbc database -> %v, want kindSchema", got)
	}
}

func TestNextKindMysqlAndClickhouseSkipSchema(t *testing.T) {
	if got := nextKind("mysql", kindDatabase); got != kindTable {
		t.Errorf("mysql database -> %v, want kindTable", got)
	}
	if got := nextKind("clickhouse", kindDatabase); got != kindTable {
		t.Errorf("clickhouse database -> %v, want kindTable", got)
	}
}

func TestNextKindProfileIsAlwaysDatabase(t *testing.T) {
	if got := nextKind("mysql", kindProfile); got != kindDatabase {
		t.Errorf("profile -> %v, want kindDatabase", got)
	}
}

func TestNextKindSchemaIsTable(t *testing.T) {
	if got := nextKind("postgres", kindSchema); got != kindTable {
		t.Errorf("schema -> %v, want kindTable", got)
	}
}

func TestNextKindTableIsColumn(t *testing.T) {
	if got := nextKind("postgres", kindTable); got != kindColumn {
		t.Errorf("table -> %v, want kindColumn", got)
	}
}

func TestLabelProfile(t *testing.T) {
	n := &node{kind: kindProfile, name: "pg-dev", adapter: "postgres"}
	if got := labelText(n); got != "pg-dev [postgres]" {
		t.Errorf("got %q", got)
	}
}

func TestLabelTableMarksViewVsTable(t *testing.T) {
	table := &node{kind: kindTable, name: "widgets", objKind: "table"}
	if got := labelText(table); got != "widgets [table]" {
		t.Errorf("table label = %q", got)
	}
	view := &node{kind: kindTable, name: "cheap_widgets", objKind: "view"}
	if got := labelText(view); got != "cheap_widgets [view]" {
		t.Errorf("view label = %q", got)
	}
}

func TestLabelTableShowsCollectionTagForMongo(t *testing.T) {
	coll := &node{kind: kindTable, name: "widgets", objKind: "collection"}
	if got := labelText(coll); got != "widgets [collection]" {
		t.Errorf("collection label = %q, want %q", got, "widgets [collection]")
	}
}

func TestLabelColumnShowsTypeAndNullability(t *testing.T) {
	n := &node{kind: kindColumn, name: "id", colType: "integer", colNullable: "NO"}
	if got := labelText(n); got != "id [integer NOT NULL]" {
		t.Errorf("got %q", got)
	}
	n2 := &node{kind: kindColumn, name: "note", colType: "text", colNullable: "YES"}
	if got := labelText(n2); got != "note [text NULL]" {
		t.Errorf("got %q", got)
	}
}

func TestLabelLoadMore(t *testing.T) {
	n := &node{kind: kindLoadMore}
	if got := labelText(n); got != "load more" {
		t.Errorf("got %q", got)
	}
}

func TestLevelChildrenBuildsTablesWithViewMarker(t *testing.T) {
	parent := &node{kind: kindSchema, name: "public", profile: "pg-dev", adapter: "postgres", path: []string{"hqdb", "public"}}
	nodes := []catalog.SchemaNode{
		{Name: "widgets", Kind: "table"},
		{Name: "cheap_widgets", Kind: "view"},
	}
	children := levelChildren(parent, nodes)
	if len(children) != 2 {
		t.Fatalf("got %d children, want 2", len(children))
	}
	if children[0].kind != kindTable || children[0].objKind != "table" {
		t.Errorf("widgets: kind=%v objKind=%q", children[0].kind, children[0].objKind)
	}
	if children[1].objKind != "view" {
		t.Errorf("cheap_widgets: objKind=%q, want view", children[1].objKind)
	}
	wantPath := []string{"hqdb", "public", "widgets"}
	if got := children[0].path; len(got) != 3 || got[0] != wantPath[0] || got[1] != wantPath[1] || got[2] != wantPath[2] {
		t.Errorf("path = %v, want %v", got, wantPath)
	}
	if children[0].parent != parent {
		t.Errorf("child.parent not set to parent")
	}
	if children[0].profile != "pg-dev" || children[0].adapter != "postgres" {
		t.Errorf("profile/adapter not propagated: %+v", children[0])
	}
}

func TestColumnChildrenBuildsLeaves(t *testing.T) {
	parent := &node{kind: kindTable, name: "widgets", profile: "pg-dev", path: []string{"hqdb", "public", "widgets"}}
	cols := []catalog.Column{
		{Name: "id", Type: "integer", Nullable: "NO"},
		{Name: "name", Type: "text", Nullable: "YES"},
	}
	children := columnChildren(parent, cols)
	if len(children) != 2 {
		t.Fatalf("got %d", len(children))
	}
	if children[0].kind != kindColumn || children[0].colType != "integer" || children[0].colNullable != "NO" {
		t.Errorf("got %+v", children[0])
	}
	if children[0].parent != parent {
		t.Errorf("parent not set")
	}
}

func TestProfileRootWalksUpToProfileNode(t *testing.T) {
	root := &node{kind: kindProfile, name: "pg-dev"}
	db := &node{kind: kindDatabase, parent: root}
	schema := &node{kind: kindSchema, parent: db}
	if got := schema.profileRoot(); got != root {
		t.Errorf("profileRoot() = %v, want root", got)
	}
	if got := root.profileRoot(); got != root {
		t.Errorf("profileRoot() of root itself should be itself")
	}
}

func TestBrokenMessagePrefersValidationError(t *testing.T) {
	n := &node{kind: kindProfile, name: "bad-dev", adapter: "postgres", validateErr: errors.New("boom")}
	got := n.brokenMessage()
	want := "Profile bad-dev (postgres) failed validation:\n\nboom"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBrokenMessageConnectError(t *testing.T) {
	n := &node{kind: kindProfile, name: "dead-dev", adapter: "postgres", connectErr: errors.New("dial tcp: refused")}
	got := n.brokenMessage()
	want := "Profile dead-dev (postgres) failed to connect:\n\ndial tcp: refused"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBrokenMessageEmptyWhenHealthy(t *testing.T) {
	n := &node{kind: kindProfile, name: "ok-dev"}
	if got := n.brokenMessage(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestIsDDLTableTrueForEverySQLAdapter(t *testing.T) {
	for _, adapter := range []string{"postgres", "mysql", "mssql", "odbc", "clickhouse"} {
		n := &node{kind: kindTable, adapter: adapter}
		if !n.isDDLTable() {
			t.Errorf("adapter %q: isDDLTable() = false, want true", adapter)
		}
	}
}

func TestIsDDLTableFalseForRedisAndMongodb(t *testing.T) {
	for _, adapter := range []string{"redis", "mongodb"} {
		n := &node{kind: kindTable, adapter: adapter}
		if n.isDDLTable() {
			t.Errorf("adapter %q: isDDLTable() = true, want false (no DDL for this adapter)", adapter)
		}
	}
}

func TestIsDDLTableFalseForNonTableKinds(t *testing.T) {
	for _, kind := range []nodeKind{kindProfile, kindDatabase, kindSchema, kindColumn, kindLoadMore} {
		n := &node{kind: kind, adapter: "postgres"}
		if n.isDDLTable() {
			t.Errorf("kind %v: isDDLTable() = true, want false (only a Table has DDL)", kind)
		}
	}
}

// labelText joins a node's label as "name [meta]", or just the name when it
// has no meta, so a test can compare both parts at once.
func labelText(n *node) string {
	name, meta := n.label()
	if meta == "" {
		return name
	}
	return name + " [" + meta + "]"
}
