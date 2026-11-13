package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Snarap1/hqdb/internal/catalog"
	"github.com/Snarap1/hqdb/internal/ddl"
)

func TestParseResultText(t *testing.T) {
	cases := []struct {
		in      string
		want    ResultText
		wantErr string
	}{
		{"auto", ResultTextAuto, ""},
		{"summary", ResultTextSummary, ""},
		{"json", ResultTextJSON, ""},
		{"full", "", `--result-text must be auto, summary, or json (got "full")`},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseResultText(c.in)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if got != c.want || gotErr != c.wantErr {
				t.Errorf("got %q, %q; want %q, %q", got, gotErr, c.want, c.wantErr)
			}
		})
	}
}

func TestSummaries(t *testing.T) {
	// Cell values that must never reach a summary: on a masked Profile
	// the structured content holds markers, and the summary holds none.
	secret := "ivan@example.ru"
	many := make([]string, 25)
	for i := range many {
		many[i] = fmt.Sprintf("c%d", i)
	}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"query", summarizeQuery(RunQueryOut{Columns: []string{"id", "email"}, Rows: [][]any{{1, secret}, {2, secret}}, RowCount: 2}),
			"2 rows, 2 columns: id, email; the data is in structuredContent"},
		{"query truncated and masked", summarizeQuery(RunQueryOut{Columns: []string{"email"}, Rows: [][]any{{secret}}, RowCount: 1, Truncated: true, MaskedColumns: []string{"email"}}),
			"1 row (truncated at the limit), 1 column: email; masked: email; the data is in structuredContent"},
		{"query with many columns", summarizeQuery(RunQueryOut{Columns: many, RowCount: 0}),
			"0 rows, 25 columns: c0, c1, c2, c3, c4, c5, c6, c7, c8, c9, c10, c11, c12, c13, c14, c15, c16, c17, c18, c19, ... (5 more); the data is in structuredContent"},
		{"mongo find", summarizeMongo(RunMongoOut{Operation: "find", Documents: []any{map[string]any{"email": secret}}, Count: 1, MaskedFields: []string{"email"}}),
			"find: 1 document; masked: email; the data is in structuredContent"},
		{"mongo count", summarizeMongo(RunMongoOut{Operation: "count", Count: 42}),
			"count: 42 matching documents; the data is in structuredContent"},
		{"mongo distinct", summarizeMongo(RunMongoOut{Operation: "distinct", Documents: []any{secret, "b"}, Truncated: true}),
			"distinct: 2 values (truncated at the limit); the data is in structuredContent"},
		{"mongo write", summarizeMongo(RunMongoOut{Operation: "insertOne", Documents: []any{map[string]any{"insertedId": secret}}}),
			"insertOne: summary document returned; the data is in structuredContent"},
		{"redis string", summarizeRedis(RunRedisOut{Command: "GET user:1", Result: secret}),
			"GET: string reply; the data is in structuredContent"},
		{"redis array", summarizeRedis(RunRedisOut{Command: "LRANGE l 0 -1", Result: []any{secret, secret}}),
			"LRANGE: array reply of 2 elements; the data is in structuredContent"},
		{"redis map", summarizeRedis(RunRedisOut{Command: "HGETALL h", Result: map[string]any{"email": secret}}),
			"HGETALL: map reply of 1 entry; the data is in structuredContent"},
		{"redis number", summarizeRedis(RunRedisOut{Command: "TTL k", Result: int64(79161234567)}),
			"TTL: number reply; the data is in structuredContent"},
		{"redis nil", summarizeRedis(RunRedisOut{Command: "GET missing", Result: nil}),
			"GET: nil reply; the data is in structuredContent"},
		{"schema", summarizeSchema(catalog.SchemaOut{Nodes: make([]catalog.SchemaNode, 3), Truncated: true}),
			"3 nodes (truncated); the data is in structuredContent"},
		{"columns", summarizeColumns(catalog.ColumnsOut{Table: "public.widgets", Columns: make([]catalog.Column, 4)}),
			"4 columns of public.widgets; the data is in structuredContent"},
		{"ddl", summarizeDDL(ddl.Out{Table: "public.widgets", Definition: "CREATE TABLE w (\n  email text DEFAULT '" + secret + "'\n);\n"}),
			"definition of public.widgets (reconstructed from the catalog), 3 lines; the data is in structuredContent"},
		{"export", summarizeExport(ExportQueryOut{Path: "/tmp/out.csv", Format: "csv", RowCount: 10}),
			"wrote 10 rows to /tmp/out.csv as csv; the data is in structuredContent"},
		{"profiles", summarizeProfiles(ListProfilesOut{Profiles: []ProfileInfo{{Name: "a"}, {Name: "b", IsDefault: true}}}),
			"2 profiles (default: b); the data is in structuredContent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got  %q\nwant %q", c.got, c.want)
			}
			if strings.Contains(c.got, secret) {
				t.Errorf("summary %q quotes a value", c.got)
			}
		})
	}
}

// fakeQueryIn is the input of the test tool registered by leanSession.
type fakeQueryIn struct {
	Rows int `json:"rows"`
}

// leanSession serves one run_query-shaped tool over in-memory transports,
// in mode, to a client named clientName.
func leanSession(t *testing.T, mode ResultText, clientName string) *mcp.ClientSession {
	t.Helper()
	old := resultText
	t.Cleanup(func() { resultText = old })
	resultText = mode
	srv := mcp.NewServer(&mcp.Implementation{Name: "hqdb", Version: "test"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "fake_query"}, func(_ context.Context, req *mcp.CallToolRequest, in fakeQueryIn) (*mcp.CallToolResult, RunQueryOut, error) {
		out := fakeRows(in.Rows)
		return lean(req, out, summarizeQuery(out)), out, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// fakeRows is an n x 8 result like the one the research benchmarks used.
func fakeRows(n int) RunQueryOut {
	cols := []string{"id", "name", "email", "created_at", "amount", "status", "note", "flag"}
	out := RunQueryOut{Columns: cols, Rows: make([][]any, n), RowCount: n}
	for i := range out.Rows {
		out.Rows[i] = []any{i, fmt.Sprintf("user %d", i), fmt.Sprintf("u%d@example.com", i),
			"2026-01-02T03:04:05Z", float64(i) * 1.5, "active", "a note with \"quotes\"", i%2 == 0}
	}
	return out
}

func callFake(t *testing.T, cs *mcp.ClientSession, rows int) (*mcp.CallToolResult, int) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "fake_query", Arguments: map[string]any{"rows": rows}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return res, len(raw)
}

func TestLeanResults(t *testing.T) {
	want := fakeRows(1000)
	wantJSON, _ := json.Marshal(want)
	cases := []struct {
		name        string
		mode        ResultText
		client      string
		wantSummary bool
	}{
		{"auto sends Claude Code a summary", ResultTextAuto, "claude-code", true},
		{"auto sends any other client the JSON", ResultTextAuto, "cursor-vscode", false},
		{"summary for every client", ResultTextSummary, "cursor-vscode", true},
		{"json for every client", ResultTextJSON, "claude-code", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, size := callFake(t, leanSession(t, c.mode, c.client), 1000)
			if res.IsError || len(res.Content) != 1 {
				t.Fatalf("result = %+v, want one content block", res)
			}
			text := res.Content[0].(*mcp.TextContent).Text
			// The structured content is intact either way.
			raw, _ := json.Marshal(res.StructuredContent)
			var got RunQueryOut
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			var wantBack RunQueryOut
			_ = json.Unmarshal(wantJSON, &wantBack)
			if !reflect.DeepEqual(got, wantBack) {
				t.Error("structuredContent differs from the handler's output")
			}
			if c.wantSummary {
				if text != summarizeQuery(want) {
					t.Errorf("text = %.80q, want the summary", text)
				}
				// The JSON appears once on the wire instead of twice.
				if size > len(wantJSON)*11/10 {
					t.Errorf("wire size %d bytes, want about the %d of one JSON copy", size, len(wantJSON))
				}
			} else {
				if text != string(raw) {
					t.Errorf("text = %.80q, want the full JSON", text)
				}
				if size < len(wantJSON)*2 {
					t.Errorf("wire size %d bytes, want the JSON twice (%d each)", size, len(wantJSON))
				}
			}
			t.Logf("%s: %d bytes on the wire for a %d-byte result", c.mode, size, len(wantJSON))
		})
	}
}

func TestLeanKeepsJSONForHugeResults(t *testing.T) {
	cs := leanSession(t, ResultTextSummary, "claude-code")
	res, _ := callFake(t, cs, 12000) // over maxSummaryJSON
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "{") {
		t.Errorf("text = %.80q, want the full JSON above %d bytes", text, maxSummaryJSON)
	}
}
