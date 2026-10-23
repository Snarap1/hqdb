package export

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/Snarap1/hqdb/internal/values"
)

// fakeDriver is a minimal database/sql driver used to prove that Query
// reaches real execution instead of refusing a statement on its own;
// screening is the MCP layer's job now (ADR-0003).
type fakeDriver struct{}

func (fakeDriver) Open(name string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(query string) (driver.Stmt, error) { return fakeStmt{}, nil }
func (fakeConn) Close() error                              { return nil }
func (fakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fakeConn: transactions not supported")
}

type fakeStmt struct{}

func (fakeStmt) Close() error  { return nil }
func (fakeStmt) NumInput() int { return -1 }
func (fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (fakeStmt) Query(args []driver.Value) (driver.Rows, error) { return fakeRows{}, nil }

// fakeRows reports one column and no rows.
type fakeRows struct{}

func (fakeRows) Columns() []string              { return []string{"n"} }
func (fakeRows) Close() error                   { return nil }
func (fakeRows) Next(dest []driver.Value) error { return io.EOF }

var registerFakeDriverOnce sync.Once

func openFakeDB(t *testing.T) *sql.DB {
	registerFakeDriverOnce.Do(func() { sql.Register("hqdb-fake-export", fakeDriver{}) })
	db, err := sql.Open("hqdb-fake-export", "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestQueryNoLongerScreens(t *testing.T) {
	// A statement that the old readonly check would have refused must now
	// reach the driver instead of being rejected inside Query.
	db := openFakeDB(t)
	dest := t.TempDir() + "/out.csv"
	n, err := Query(context.Background(), db, "DROP TABLE widgets", dest, "csv", 0)
	if err != nil {
		t.Fatalf("Query returned an error instead of executing: %v", err)
	}
	if n != 0 {
		t.Errorf("row count = %d, want 0", n)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("destination file was not written: %v", err)
	}
}

func TestQueryMissingParentDir(t *testing.T) {
	_, err := Query(context.Background(), nil, "SELECT 1", "/tmp/hqdb-does-not-exist/out.csv", "csv", 0)
	if err == nil {
		t.Fatal("expected an error for a missing parent directory")
	}
	if want := "parent directory does not exist: /tmp/hqdb-does-not-exist"; err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestQueryRespectsCancelledContext(t *testing.T) {
	// A context cancelled before the call must stop Query from ever reaching
	// the driver, proving ctx is threaded through to the connection and not
	// substituted with context.Background().
	db := openFakeDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Query(ctx, db, "SELECT 1", t.TempDir()+"/out.csv", "csv", 0)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want a context.Canceled error", err)
	}
}

func TestInferFormat(t *testing.T) {
	cases := map[string]string{
		"/tmp/out.csv":    "csv",
		"/tmp/out.tsv":    "csv",
		"/tmp/out.json":   "json",
		"/tmp/out.ndjson": "ndjson",
	}
	for path, want := range cases {
		got, err := InferFormat(path)
		if err != nil {
			t.Fatalf("InferFormat(%q): %v", path, err)
		}
		if got != want {
			t.Errorf("InferFormat(%q) = %q, want %q", path, got, want)
		}
	}
	if _, err := InferFormat("/tmp/out.txt"); err == nil ||
		err.Error() != `cannot infer format from extension ".txt"; pass format explicitly` {
		t.Errorf("unexpected error for an unknown extension: %v", err)
	}
}

func TestStringify(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"x", "x"},
		{true, "true"},
		{int64(12), "12"},
		{1.5, "1.5"},
		{[]any{"a", int64(1)}, `["a",1]`},
	}
	for _, tc := range cases {
		if got := stringify(tc.in); got != tc.want {
			t.Errorf("stringify(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// docSource streams docs the way a mongodb cursor would.
func docSource(docs ...any) func(func(any) error) error {
	return func(emit func(any) error) error {
		for _, d := range docs {
			if err := emit(d); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestDocumentsWritesEveryFormatInServerFieldOrder(t *testing.T) {
	docs := []any{
		values.Doc{{Key: "name", Value: "bolt"}, {Key: "_id", Value: values.Doc{{Key: "$oid", Value: "65a1"}}}, {Key: "dims", Value: values.Doc{{Key: "w", Value: 1.0}, {Key: "h", Value: 2.0}}}},
		values.Doc{{Key: "_id", Value: values.Doc{{Key: "$oid", Value: "65a2"}}}, {Key: "tags", Value: []any{"a"}}, {Key: "name", Value: "nut"}},
	}
	dir := t.TempDir()
	cases := []struct{ format, want string }{
		{"csv", "_id,name,dims,tags\n\"{\"\"$oid\"\":\"\"65a1\"\"}\",bolt,\"{\"\"w\"\":1,\"\"h\"\":2}\",\n\"{\"\"$oid\"\":\"\"65a2\"\"}\",nut,,\"[\"\"a\"\"]\"\n"},
		{"json", "[{\"name\":\"bolt\",\"_id\":{\"$oid\":\"65a1\"},\"dims\":{\"w\":1,\"h\":2}}\n,{\"_id\":{\"$oid\":\"65a2\"},\"tags\":[\"a\"],\"name\":\"nut\"}\n]\n"},
		{"ndjson", "{\"name\":\"bolt\",\"_id\":{\"$oid\":\"65a1\"},\"dims\":{\"w\":1,\"h\":2}}\n{\"_id\":{\"$oid\":\"65a2\"},\"tags\":[\"a\"],\"name\":\"nut\"}\n"},
	}
	for _, c := range cases {
		p := filepath.Join(dir, "out."+c.format)
		n, err := Documents(p, c.format, docSource(docs...))
		if err != nil || n != 2 {
			t.Fatalf("%s: n=%d err=%v", c.format, n, err)
		}
		got, _ := os.ReadFile(p)
		if string(got) != c.want {
			t.Errorf("%s:\ngot  %q\nwant %q", c.format, got, c.want)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != len(cases) {
		t.Errorf("export left extra files behind: %v", entries)
	}
	if _, err := Documents(filepath.Join(dir, "missing", "x.csv"), "csv", docSource(docs...)); err == nil {
		t.Error("missing parent dir must fail")
	}
}

func TestDocumentsReportsTheSourceError(t *testing.T) {
	boom := errors.New("cursor died")
	src := func(emit func(any) error) error {
		if err := emit(values.Doc{{Key: "a", Value: 1.0}}); err != nil {
			return err
		}
		return boom
	}
	for _, format := range Formats {
		p := filepath.Join(t.TempDir(), "out."+format)
		if _, err := Documents(p, format, src); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want %v", format, err, boom)
		}
	}
}

func TestDocumentFieldsIDFirstThenFirstSeen(t *testing.T) {
	docs := []any{
		values.Doc{{Key: "z", Value: 1}, {Key: "_id", Value: 1}, {Key: "a", Value: 1}},
		values.Doc{{Key: "m", Value: 1}, {Key: "z", Value: 1}},
	}
	got := DocumentFields(docs)
	want := []string{"_id", "z", "a", "m"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DocumentFields = %v, want %v", got, want)
	}
}
