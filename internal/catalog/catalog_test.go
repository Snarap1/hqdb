package catalog

import (
	"testing"
)

// fakeRedis is a redis connection stub: GetColumns and GetSchema reject it on
// the adapter kind alone, before any client call.
type fakeRedis struct{}

func (fakeRedis) Kind() string { return "redis" }
func (fakeRedis) Close()       {}

func TestGetColumnsRejectsRedis(t *testing.T) {
	_, err := GetColumns(t.Context(), fakeRedis{}, []string{"user"})
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "get_columns is not available for redis; use get_schema"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestPlaceholders(t *testing.T) {
	if got := placeholders(3); got != "?, ?, ?" {
		t.Errorf("got %q", got)
	}
	if got := placeholders(1); got != "?" {
		t.Errorf("got %q", got)
	}
}

func TestLikeFilter(t *testing.T) {
	if got := likeFilter(""); got != "%%" {
		t.Errorf("got %q, want %%%%", got)
	}
	if got := likeFilter("wid"); got != "%wid%" {
		t.Errorf("got %q, want %%wid%%", got)
	}
}

func TestDialect(t *testing.T) {
	for kind, want := range map[string]string{
		"odbc": "mssql", "mssql": "mssql", "postgres": "postgres", "mysql": "mysql", "clickhouse": "clickhouse",
	} {
		if got := dialect(kind); got != want {
			t.Errorf("dialect(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestColumnsUsageExamples(t *testing.T) {
	if got := columnsUsage("odbc"); got != `["hqdb", "public", "widgets"]` {
		t.Errorf("odbc usage = %q", got)
	}
	if got := columnsUsage("postgres"); got != `["hqdb", "public", "widgets"]` {
		t.Errorf("postgres usage = %q", got)
	}
	if got := columnsUsage("clickhouse"); got != `["hqdb", "widgets"]` {
		t.Errorf("clickhouse usage = %q", got)
	}
}

func TestRebindPlaceholdersForPostgres(t *testing.T) {
	// pgx needs $n; the other drivers keep '?'.
	if got := rebindPlaceholders("SELECT a FROM t WHERE b = ? AND c = ?", "postgres"); got != "SELECT a FROM t WHERE b = $1 AND c = $2" {
		t.Errorf("postgres: %q", got)
	}
	if got := rebindPlaceholders("SELECT a FROM t WHERE b = ?", "mysql"); got != "SELECT a FROM t WHERE b = ?" {
		t.Errorf("mysql: %q", got)
	}
}

func namedNodes(names ...string) []SchemaNode {
	nodes := make([]SchemaNode, len(names))
	for i, n := range names {
		nodes[i] = SchemaNode{Name: n, Kind: "table"}
	}
	return nodes
}

func TestPaginateNoLimitsReturnsEverything(t *testing.T) {
	nodes := namedNodes("a", "b", "c")
	got, more := paginate(nodes, Limits{})
	if len(got) != 3 || more {
		t.Errorf("got %+v, more=%v; want all 3 nodes and more=false", got, more)
	}
}

func TestPaginateNodeCapTruncates(t *testing.T) {
	nodes := namedNodes("a", "b", "c")
	got, more := paginate(nodes, Limits{NodeCap: 2})
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" || !more {
		t.Errorf("got %+v, more=%v; want [a b] and more=true", got, more)
	}
}

func TestPaginateOffsetReturnsNextChunkInStableOrder(t *testing.T) {
	nodes := namedNodes("a", "b", "c", "d", "e")
	got, more := paginate(nodes, Limits{NodeCap: 2, Offset: 2})
	if len(got) != 2 || got[0].Name != "c" || got[1].Name != "d" || !more {
		t.Errorf("got %+v, more=%v; want [c d] and more=true", got, more)
	}
	got, more = paginate(nodes, Limits{NodeCap: 2, Offset: 4})
	if len(got) != 1 || got[0].Name != "e" || more {
		t.Errorf("got %+v, more=%v; want [e] and more=false", got, more)
	}
}

func TestPaginateOffsetBeyondLengthReturnsEmpty(t *testing.T) {
	nodes := namedNodes("a", "b")
	got, more := paginate(nodes, Limits{NodeCap: 2, Offset: 10})
	if len(got) != 0 || more {
		t.Errorf("got %+v, more=%v; want empty and more=false", got, more)
	}
}

func TestOverCapZeroMeansNoCap(t *testing.T) {
	out := &SchemaOut{Nodes: namedNodes("a", "b", "c")}
	if overCap(out, Limits{CharCap: 0}) {
		t.Error("CharCap of 0 must mean no payload cap")
	}
}

func TestOverCapExceeded(t *testing.T) {
	out := &SchemaOut{Nodes: namedNodes("a", "b", "c")}
	if !overCap(out, Limits{CharCap: 5}) {
		t.Error("want over cap for a tiny CharCap")
	}
	if overCap(out, Limits{CharCap: 100000}) {
		t.Error("want not over cap for a generous CharCap")
	}
}
