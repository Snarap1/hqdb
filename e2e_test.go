package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Snarap1/hqdb/internal/catalog"
	"github.com/Snarap1/hqdb/internal/ddl"
	"github.com/Snarap1/hqdb/internal/server"
)

// e2eSession is a client session against the built hqdb binary, configured
// with the loopback docker fixtures.
type e2eSession struct {
	t    *testing.T
	sess *mcp.ClientSession
}

// startE2E builds the binary, writes the dev profile config into a temp dir,
// and connects a client to it with that dir as the working directory.
func startE2E(t *testing.T) *e2eSession {
	t.Helper()
	if os.Getenv("HQDB_E2E") != "1" {
		t.Skip("set HQDB_E2E=1 and run bash testdata/fixtures.sh up first")
	}
	bin := filepath.Join(t.TempDir(), "hqdb")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build hqdb: %v", err)
	}
	dir := t.TempDir()
	cfg, err := os.ReadFile(filepath.Join("testdata", "fixtures.toml"))
	if err != nil {
		t.Fatalf("read fixtures.toml: %v", err)
	}
	// pg-rw is pg-dev with read-write access: the same database, unscreened.
	cfg = append(cfg, []byte(`
[profiles.pg-rw]
adapter = "postgres"
access = "read-write"
host = "127.0.0.1"
port = 55432
user = "hq"
password = "hq"
dbname = "hqdb"
`)...)
	cfgPath := filepath.Join(dir, "hqdb.toml")
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HQDB_CONFIG="+cfgPath)
	client := mcp.NewClient(&mcp.Implementation{Name: "hqdb-e2e", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	sess, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to hqdb: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return &e2eSession{t: t, sess: sess}
}

// call runs one tool and fails the test on a protocol-level error.
func (s *e2eSession) call(name string, args map[string]any) *mcp.CallToolResult {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		s.t.Fatalf("%s: %v", name, err)
	}
	return res
}

// ok calls a tool, fails on IsError, and decodes the structured output.
func (s *e2eSession) ok(name string, args map[string]any, out any) {
	s.t.Helper()
	res := s.call(name, args)
	if res.IsError {
		s.t.Fatalf("%s returned an error: %s", name, textOf(res))
	}
	if out == nil {
		return
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		s.t.Fatalf("marshal %s output: %v", name, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		s.t.Fatalf("decode %s output %s: %v", name, raw, err)
	}
}

// errText calls a tool expecting a refusal and returns the message.
func (s *e2eSession) errText(name string, args map[string]any) string {
	s.t.Helper()
	res := s.call(name, args)
	if !res.IsError {
		s.t.Fatalf("%s should have failed, got %s", name, textOf(res))
	}
	return textOf(res)
}

// textOf concatenates the text content blocks of a tool result.
func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestE2EListProfiles(t *testing.T) {
	s := startE2E(t)
	var out server.ListProfilesOut
	s.ok("list_profiles", map[string]any{}, &out)
	got := map[string]server.ProfileInfo{}
	for _, p := range out.Profiles {
		got[p.Name] = p
	}
	for _, w := range []string{"pg-dev", "my-dev", "ms-dev", "ch-dev", "redis-dev", "mongo-dev"} {
		if _, ok := got[w]; !ok {
			t.Errorf("profile %q missing from %+v", w, out.Profiles)
		}
	}
	if p := got["pg-dev"]; !p.IsDefault || p.Adapter != "postgres" || p.Access != "read-only" {
		t.Errorf("pg-dev = %+v, want the default read-only postgres profile", p)
	}
	if p := got["pg-rw"]; p.Access != "read-write" {
		t.Errorf("pg-rw = %+v, want read-write", p)
	}
}

// TestE2EReadWriteProfileRunsUnscreened runs a statement the screen refuses
// on the read-only pg-dev and runs as-is on the read-write pg-rw, which
// points at the same database. A temp table dies with the connection, so
// the write leaves nothing behind.
func TestE2EReadWriteProfileRunsUnscreened(t *testing.T) {
	s := startE2E(t)
	const write = "CREATE TEMP TABLE hqdb_rw_probe (a int)"
	msg := s.errText("run_query", map[string]any{"profile": "pg-dev", "sql": write})
	if !strings.HasPrefix(msg, "Refused (read-only profile): ") {
		t.Errorf("pg-dev: message = %q, want the read-only refusal", msg)
	}
	var out server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": "pg-rw", "sql": write}, &out)
}

func TestE2ERunQueryAdapters(t *testing.T) {
	s := startE2E(t)
	for _, profile := range []string{"pg-dev", "my-dev", "ms-dev", "ch-dev", "ch-native-dev", "ms-odbc-dev"} {
		var out server.RunQueryOut
		s.ok("run_query", map[string]any{"profile": profile, "sql": "SELECT 1 AS one"}, &out)
		if len(out.Rows) != 1 || len(out.Rows[0]) != 1 {
			t.Fatalf("%s: rows = %+v, want [[1]]", profile, out.Rows)
		}
		if out.Rows[0][0] != float64(1) {
			t.Errorf("%s: row = %+v, want [1]", profile, out.Rows[0])
		}
		if out.Columns[0] != "one" {
			t.Errorf("%s: columns = %v, want [one]", profile, out.Columns)
		}
	}
}

func TestE2ERunQueryClickHouseVersion(t *testing.T) {
	s := startE2E(t)
	for _, profile := range []string{"ch-dev", "ch-native-dev"} {
		var out server.RunQueryOut
		s.ok("run_query", map[string]any{"profile": profile, "sql": "SELECT version()"}, &out)
		if len(out.Rows) != 1 {
			t.Fatalf("%s: rows = %+v, want one row", profile, out.Rows)
		}
		if v, ok := out.Rows[0][0].(string); !ok || v == "" {
			t.Errorf("%s: version = %#v, want a non-empty string", profile, out.Rows[0][0])
		}
	}
}

func TestE2ERunQueryLimit(t *testing.T) {
	s := startE2E(t)
	var out server.RunQueryOut
	s.ok("run_query", map[string]any{
		"profile": "pg-dev", "sql": "SELECT id FROM widgets ORDER BY id", "limit": 2,
	}, &out)
	if out.RowCount != 2 || !out.Truncated {
		t.Errorf("row_count = %d, truncated = %v; want 2 and true", out.RowCount, out.Truncated)
	}
}

func TestE2ERunQueryRefusesWrite(t *testing.T) {
	s := startE2E(t)
	msg := s.errText("run_query", map[string]any{"profile": "pg-dev", "sql": "DELETE FROM widgets"})
	if !strings.HasPrefix(msg, "Refused (read-only profile): ") {
		t.Errorf("message = %q, want the read-only refusal prefix", msg)
	}
}

// TestE2ERunQueryCancellation proves that cancelling the MCP request context
// stops the underlying query server-side, not just that the client gives up
// waiting for it. A client-side cancel always makes CallTool return early,
// so the real proof is that the postgres backend running pg_sleep is gone
// shortly after, instead of sleeping for its full duration.
func TestE2ERunQueryCancellation(t *testing.T) {
	s := startE2E(t)

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := s.sess.CallTool(ctx, &mcp.CallToolParams{
			Name:      "run_query",
			Arguments: map[string]any{"profile": "pg-dev", "sql": "SELECT pg_sleep(5)"},
		})
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the cancelled call to return an error")
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("cancelled call took %v, want it to return promptly", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled run_query did not return promptly")
	}

	// Give the server a moment to act on the cancellation, then confirm the
	// pg_sleep statement is no longer running: if the core had ignored the
	// context, this would still find it active for the rest of the 5s sleep.
	// The query is matched exactly (not with a substring LIKE) so that this
	// very check cannot match itself, and pg_backend_pid() excludes it too.
	time.Sleep(1 * time.Second)
	var out server.RunQueryOut
	s.ok("run_query", map[string]any{
		"profile": "pg-dev",
		"sql": "SELECT count(*) FROM pg_stat_activity " +
			"WHERE state = 'active' AND pid <> pg_backend_pid() AND query = 'SELECT pg_sleep(5)'",
	}, &out)
	if out.Rows[0][0] != float64(0) {
		t.Errorf("a pg_sleep query is still running server-side after cancellation; got %v active", out.Rows[0][0])
	}
}

func TestE2ERunQueryOnRedisProfileFails(t *testing.T) {
	s := startE2E(t)
	msg := s.errText("run_query", map[string]any{"profile": "redis-dev", "sql": "SELECT 1"})
	if !strings.Contains(msg, "run_query is not available for redis") {
		t.Errorf("message = %q", msg)
	}
}

func TestE2EGetSchemaPostgres(t *testing.T) {
	s := startE2E(t)
	var root catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "pg-dev", "path": []string{}}, &root)
	// pg-dev's dbname is where discovery starts; it still lists the others.
	if names := nodeNames(root.Nodes); !names["hqdb"] || !names["hqdb_two"] || root.Nodes[0].Kind != "database" {
		t.Fatalf("root nodes = %+v, want the hqdb and hqdb_two databases", root.Nodes)
	}
	var schemas catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "pg-dev", "path": []string{"hqdb"}}, &schemas)
	names := nodeNames(schemas.Nodes)
	if !names["analytics"] || !names["public"] {
		t.Errorf("schema nodes = %+v, want analytics and public", schemas.Nodes)
	}
	var tables catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "pg-dev", "path": []string{"hqdb", "public"}}, &tables)
	// The TUI suite may create its own short-lived tables in public at the
	// same time, so look for widgets rather than expecting it alone.
	var widgets *catalog.SchemaNode
	for i, n := range tables.Nodes {
		if n.Name == "widgets" {
			widgets = &tables.Nodes[i]
		}
	}
	if widgets == nil || widgets.Kind != "table" {
		t.Errorf("table nodes = %+v, want widgets (table)", tables.Nodes)
	}
	var filtered catalog.SchemaOut
	s.ok("get_schema", map[string]any{
		"profile": "pg-dev", "path": []string{"hqdb", "analytics"}, "name_filter": "wid",
	}, &filtered)
	if !nodeNames(filtered.Nodes)["widgets_extra"] {
		t.Errorf("filtered nodes = %+v, want widgets_extra", filtered.Nodes)
	}
}

func TestE2EGetColumns(t *testing.T) {
	s := startE2E(t)
	var out catalog.ColumnsOut
	s.ok("get_columns", map[string]any{
		"profile": "pg-dev", "path": []string{"hqdb", "public", "widgets"},
	}, &out)
	if out.Table != "public.widgets" {
		t.Errorf("table = %q, want public.widgets", out.Table)
	}
	cols := map[string]catalog.Column{}
	for _, c := range out.Columns {
		cols[c.Name] = c
	}
	if _, ok := cols["price"]; !ok {
		t.Errorf("columns = %+v, want a price column", out.Columns)
	}
	if c := cols["name"]; c.Nullable != "YES" {
		t.Errorf("widgets.name nullable = %q, want YES", c.Nullable)
	}
}

func TestE2EGetColumnsClickHouseNullability(t *testing.T) {
	s := startE2E(t)
	var out catalog.ColumnsOut
	s.ok("get_columns", map[string]any{
		"profile": "ch-dev", "path": []string{"hqdb", "widgets"},
	}, &out)
	cols := map[string]catalog.Column{}
	for _, c := range out.Columns {
		cols[c.Name] = c
	}
	if c := cols["id"]; c.Nullable != "NO" {
		t.Errorf("id column = %+v, want nullable NO", c)
	}
	if c := cols["note"]; c.Nullable != "YES" {
		t.Errorf("note column = %+v, want nullable YES", c)
	}
}

func TestE2EGetSchemaRedis(t *testing.T) {
	s := startE2E(t)
	var out catalog.SchemaOut
	s.ok("get_schema", map[string]any{
		"profile": "redis-dev", "path": []string{}, "include_columns": true,
	}, &out)
	nodes := map[string]catalog.SchemaNode{}
	for _, n := range out.Nodes {
		nodes[n.Name] = n
	}
	if u, ok := nodes["user"]; !ok || u.Kind != "namespace" || u.Count != 2 {
		t.Errorf("user node = %+v, want a namespace with 2 keys", u)
	}
	if p, ok := nodes["plainkey"]; !ok || p.Kind != "key" || p.Type != "string" {
		t.Errorf("plainkey node = %+v, want a string key", p)
	}
}

func TestE2EGetSchemaClickHouse(t *testing.T) {
	s := startE2E(t)
	var root catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "ch-dev", "path": []string{}}, &root)
	if !nodeNames(root.Nodes)["hqdb"] {
		t.Errorf("root nodes = %+v, want hqdb", root.Nodes)
	}
	var tables catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "ch-dev", "path": []string{"hqdb"}}, &tables)
	if !nodeNames(tables.Nodes)["widgets"] {
		t.Errorf("table nodes = %+v, want widgets", tables.Nodes)
	}
}

func TestE2EGetSchemaMySQL(t *testing.T) {
	s := startE2E(t)
	var root catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "my-dev", "path": []string{}}, &root)
	if !nodeNames(root.Nodes)["hqdb2"] {
		t.Errorf("root nodes = %+v, want hqdb2", root.Nodes)
	}
	var tables catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "my-dev", "path": []string{"hqdb"}}, &tables)
	if !nodeNames(tables.Nodes)["widgets"] {
		t.Errorf("table nodes = %+v, want widgets", tables.Nodes)
	}
}

func TestE2EGetSchemaMSSQLAndODBC(t *testing.T) {
	s := startE2E(t)
	for _, profile := range []string{"ms-dev", "ms-odbc-dev"} {
		var root catalog.SchemaOut
		s.ok("get_schema", map[string]any{"profile": profile, "path": []string{}}, &root)
		if !nodeNames(root.Nodes)["hqdb"] {
			t.Errorf("%s: root nodes = %+v, want hqdb", profile, root.Nodes)
		}
		var schemas catalog.SchemaOut
		s.ok("get_schema", map[string]any{"profile": profile, "path": []string{"hqdb"}}, &schemas)
		if names := nodeNames(schemas.Nodes); !names["dbo"] || names["sys"] {
			t.Errorf("%s: schema nodes = %+v, want dbo without sys", profile, schemas.Nodes)
		}
		var tables catalog.SchemaOut
		s.ok("get_schema", map[string]any{"profile": profile, "path": []string{"hqdb", "dbo"}}, &tables)
		if !nodeNames(tables.Nodes)["widgets"] {
			t.Errorf("%s: table nodes = %+v, want widgets", profile, tables.Nodes)
		}
		var cols catalog.ColumnsOut
		s.ok("get_columns", map[string]any{"profile": profile, "path": []string{"hqdb", "dbo", "widgets"}}, &cols)
		if len(cols.Columns) == 0 {
			t.Errorf("%s: columns = %+v, want widgets columns", profile, cols)
		}
	}
}

func TestE2EExportQuery(t *testing.T) {
	s := startE2E(t)
	dest := filepath.Join(t.TempDir(), "out.csv")
	var out server.ExportQueryOut
	s.ok("export_query", map[string]any{
		"profile": "pg-dev", "sql": "SELECT 1 AS one", "dest_path": dest,
	}, &out)
	if out.Path != dest || out.Format != "csv" || out.RowCount != 1 {
		t.Errorf("export result = %+v", out)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "one\n1" {
		t.Errorf("file content = %q, want header one and row 1", got)
	}
}

func TestE2EExportQueryJSONAndNDJSON(t *testing.T) {
	s := startE2E(t)
	dir := t.TempDir()
	var out server.ExportQueryOut
	s.ok("export_query", map[string]any{
		"profile": "pg-dev", "sql": "SELECT 1 AS one", "dest_path": filepath.Join(dir, "out.json"),
	}, &out)
	if out.Format != "json" {
		t.Errorf("format = %q, want json", out.Format)
	}
	data, err := os.ReadFile(out.Path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("json export %s: %v", data, err)
	}
	if len(rows) != 1 || rows[0]["one"] != float64(1) {
		t.Errorf("json rows = %+v", rows)
	}
	s.ok("export_query", map[string]any{
		"profile": "pg-dev", "sql": "SELECT 1 AS one", "dest_path": filepath.Join(dir, "out.ndjson"),
	}, &out)
	data, err = os.ReadFile(out.Path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimRight(string(data), "\n"), "\n"); lines != 0 {
		t.Errorf("ndjson content = %q, want a single line", data)
	}
	if !strings.Contains(string(data), `"one":1`) {
		t.Errorf("ndjson content = %q, want the one column", data)
	}
}

func TestE2EExportQueryRefusesWrite(t *testing.T) {
	s := startE2E(t)
	dest := filepath.Join(t.TempDir(), "out.csv")
	msg := s.errText("export_query", map[string]any{
		"profile": "pg-dev", "sql": "TRUNCATE widgets", "dest_path": dest,
	})
	if !strings.HasPrefix(msg, "Refused (read-only profile): ") {
		t.Errorf("message = %q", msg)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("a refused export must not create the destination file")
	}
}

func TestE2ERunRedis(t *testing.T) {
	s := startE2E(t)
	var got server.RunRedisOut
	s.ok("run_redis", map[string]any{
		"profile": "redis-dev", "command": "GET user:1000",
	}, &got)
	if got.Result != `{"id":1000}` {
		t.Errorf("result = %#v, want the user JSON", got.Result)
	}
	var scan server.RunRedisOut
	s.ok("run_redis", map[string]any{
		"profile": "redis-dev", "command": "SCAN", "args": []string{"0", "MATCH", "user:*", "COUNT", "100"},
	}, &scan)
	// SCAN replies with a [cursor, keys] pair.
	pair, ok := scan.Result.([]any)
	if !ok || len(pair) != 2 {
		t.Fatalf("scan result = %#v, want a [cursor, keys] pair", scan.Result)
	}
	keyList, ok := pair[1].([]any)
	if !ok {
		t.Fatalf("scan keys = %#v, want an array", pair[1])
	}
	found := map[string]bool{}
	for _, k := range keyList {
		found[fmt.Sprint(k)] = true
	}
	if !found["user:1000"] || !found["user:1000:roles"] {
		t.Errorf("scan keys = %v, want user:1000 and user:1000:roles", keyList)
	}
	msg := s.errText("run_redis", map[string]any{
		"profile": "redis-dev", "command": "DEL", "args": []string{"user:1000"},
	})
	if msg != `Refused (read-only profile): command "DEL" is not allowed; this profile permits read commands only` {
		t.Errorf("message = %q", msg)
	}
	// SET writes too, so a read-only Profile refuses it like any other write.
	if msg := s.errText("run_redis", map[string]any{
		"profile": "redis-dev", "command": "SET", "args": []string{"hqdb:e2e:tmp", "v1", "EX", "60"},
	}); !strings.Contains(msg, "this profile permits read commands only") {
		t.Errorf("SET message = %q", msg)
	}
}

func TestE2EGetColumnsRedisFails(t *testing.T) {
	s := startE2E(t)
	msg := s.errText("get_columns", map[string]any{"profile": "redis-dev", "path": []string{"plainkey"}})
	if !strings.Contains(msg, "get_columns is not available for redis") {
		t.Errorf("message = %q", msg)
	}
}

func TestE2EUnknownProfile(t *testing.T) {
	s := startE2E(t)
	msg := s.errText("run_query", map[string]any{"profile": "nope", "sql": "SELECT 1"})
	if !strings.Contains(msg, `unknown profile "nope"; run list_profiles`) {
		t.Errorf("message = %q", msg)
	}
}

// nodeNames indexes node names for assertions.
func nodeNames(nodes []catalog.SchemaNode) map[string]bool {
	out := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		out[n.Name] = true
	}
	return out
}

func TestE2ERunMongo(t *testing.T) {
	s := startE2E(t)
	var found server.RunMongoOut
	s.ok("run_mongo", map[string]any{
		"profile": "mongo-dev", "collection": "widgets",
		"filter": `{"price": {"$gt": 1}}`, "projection": `{"name": 1}`, "sort": `{"price": -1}`,
	}, &found)
	if found.Count != 2 || len(found.Documents) != 2 {
		t.Fatalf("find = %+v, want 2 documents", found)
	}
	if first, _ := found.Documents[0].(map[string]any); first["name"] != "nut" {
		t.Errorf("first document = %v, want nut (sorted by price desc)", found.Documents[0])
	}

	var limited server.RunMongoOut
	s.ok("run_mongo", map[string]any{"profile": "mongo-dev", "collection": "widgets", "limit": 2}, &limited)
	if !limited.Truncated || limited.Count != 2 {
		t.Errorf("find limit 2 = %+v, want 2 documents and truncated", limited)
	}

	var count server.RunMongoOut
	s.ok("run_mongo", map[string]any{
		"profile": "mongo-dev", "collection": "widgets", "operation": "count", "filter": `{"price": {"$lt": 2}}`,
	}, &count)
	if count.Count != 2 {
		t.Errorf("count = %+v, want 2", count)
	}

	var distinct server.RunMongoOut
	s.ok("run_mongo", map[string]any{
		"profile": "mongo-dev", "collection": "widgets", "operation": "distinct", "field": "name",
	}, &distinct)
	if distinct.Count != 3 {
		t.Errorf("distinct = %+v, want 3 names", distinct)
	}

	var agg server.RunMongoOut
	s.ok("run_mongo", map[string]any{
		"profile": "mongo-dev", "collection": "widgets", "operation": "aggregate",
		"pipeline": `[{"$group": {"_id": null, "total": {"$sum": "$price"}}}]`,
	}, &agg)
	if len(agg.Documents) != 1 {
		t.Fatalf("aggregate = %+v, want one document", agg)
	}
	if doc, _ := agg.Documents[0].(map[string]any); doc["total"] != 4.5 {
		t.Errorf("aggregate total = %v, want 4.5", agg.Documents[0])
	}

	msg := s.errText("run_mongo", map[string]any{
		"profile": "mongo-dev", "collection": "widgets", "operation": "aggregate",
		"pipeline": `[{"$match": {}}, {"$out": "stolen"}]`,
	})
	if !strings.Contains(msg, `Refused (read-only profile): aggregation stage "$out" writes data`) {
		t.Errorf("$out refusal = %q", msg)
	}
}

func TestE2EGetSchemaMongo(t *testing.T) {
	s := startE2E(t)
	var root catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "mongo-dev", "path": []string{}}, &root)
	if names := nodeNames(root.Nodes); !names["hqdb"] || names["admin"] {
		t.Errorf("root nodes = %+v, want hqdb and no system databases", root.Nodes)
	}
	var colls catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "mongo-dev", "path": []string{"hqdb"}}, &colls)
	kinds := map[string]string{}
	for _, n := range colls.Nodes {
		kinds[n.Name] = n.Kind
	}
	if kinds["widgets"] != "collection" || kinds["cheap_widgets"] != "view" {
		t.Errorf("collection nodes = %+v, want widgets collection and cheap_widgets view", colls.Nodes)
	}
}

func TestE2EGetColumnsMongo(t *testing.T) {
	s := startE2E(t)
	var out catalog.ColumnsOut
	s.ok("get_columns", map[string]any{"profile": "mongo-dev", "path": []string{"hqdb", "widgets"}}, &out)
	cols := map[string]catalog.Column{}
	for _, c := range out.Columns {
		cols[c.Name] = c
	}
	if c := cols["name"]; c.Type != "string" || c.Nullable != "NO" {
		t.Errorf("name column = %+v, want string, not nullable", c)
	}
	if c := cols["tags"]; c.Type != "array" || c.Nullable != "YES" {
		t.Errorf("tags column = %+v, want array, nullable (missing in some documents)", c)
	}
}

func TestE2EMongoCrossAdapter(t *testing.T) {
	s := startE2E(t)
	if msg := s.errText("run_query", map[string]any{"profile": "mongo-dev", "sql": "SELECT 1"}); !strings.Contains(msg, "run_query is not available for mongodb; use run_mongo") {
		t.Errorf("run_query on mongodb = %q", msg)
	}
	if msg := s.errText("run_mongo", map[string]any{"profile": "pg-dev", "collection": "widgets"}); !strings.Contains(msg, "run_mongo is not available for postgres") {
		t.Errorf("run_mongo on postgres = %q", msg)
	}
}

func TestE2EGetDDLPostgresTable(t *testing.T) {
	s := startE2E(t)
	var out ddl.Out
	s.ok("get_ddl", map[string]any{"profile": "pg-dev", "path": []string{"hqdb", "public", "widgets"}}, &out)
	if out.Native {
		t.Errorf("postgres table ddl should be reconstructed, got native = %v", out.Native)
	}
	if !strings.Contains(out.Definition, `CREATE TABLE "public"."widgets"`) {
		t.Errorf("definition = %q, want a CREATE TABLE for public.widgets", out.Definition)
	}
	if !strings.Contains(out.Definition, `"id"`) || !strings.Contains(out.Definition, "PRIMARY KEY") {
		t.Errorf("definition = %q, want an id column and a primary key", out.Definition)
	}
}

func TestE2EGetDDLPostgresView(t *testing.T) {
	s := startE2E(t)
	var out ddl.Out
	s.ok("get_ddl", map[string]any{"profile": "pg-dev", "path": []string{"hqdb", "analytics", "cheap_widgets"}}, &out)
	if !out.Native {
		t.Errorf("a view's definition should be native, got native = %v", out.Native)
	}
	if !strings.Contains(strings.ToUpper(out.Definition), "SELECT") {
		t.Errorf("definition = %q, want the view's SELECT", out.Definition)
	}
}

func TestE2EGetDDLMSSQLAndODBCTable(t *testing.T) {
	s := startE2E(t)
	for _, profile := range []string{"ms-dev", "ms-odbc-dev"} {
		var out ddl.Out
		s.ok("get_ddl", map[string]any{"profile": profile, "path": []string{"hqdb", "dbo", "widgets"}}, &out)
		if out.Native {
			t.Errorf("%s: mssql table ddl should be reconstructed, got native = %v", profile, out.Native)
		}
		if !strings.Contains(out.Definition, `CREATE TABLE [dbo].[widgets]`) {
			t.Errorf("%s: definition = %q, want a CREATE TABLE for dbo.widgets", profile, out.Definition)
		}
		if !strings.Contains(out.Definition, "PRIMARY KEY") {
			t.Errorf("%s: definition = %q, want a primary key", profile, out.Definition)
		}
	}
}

func TestE2EGetDDLClickHouseNative(t *testing.T) {
	s := startE2E(t)
	var out ddl.Out
	s.ok("get_ddl", map[string]any{"profile": "ch-dev", "path": []string{"hqdb", "widgets"}}, &out)
	if !out.Native {
		t.Errorf("clickhouse ddl should be native, got native = %v", out.Native)
	}
	if !strings.Contains(out.Definition, "CREATE TABLE") || !strings.Contains(out.Definition, "widgets") {
		t.Errorf("definition = %q, want a CREATE TABLE for widgets", out.Definition)
	}
}

func TestE2EGetDDLUnsupportedAdapters(t *testing.T) {
	s := startE2E(t)
	if msg := s.errText("get_ddl", map[string]any{"profile": "redis-dev", "path": []string{"plainkey"}}); !strings.Contains(msg, "get_ddl is not available for redis; use run_redis") {
		t.Errorf("redis message = %q", msg)
	}
	if msg := s.errText("get_ddl", map[string]any{"profile": "mongo-dev", "path": []string{"hqdb", "widgets"}}); !strings.Contains(msg, "get_ddl is not available for mongodb; use run_mongo") {
		t.Errorf("mongodb message = %q", msg)
	}
}
