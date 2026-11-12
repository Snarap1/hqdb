package export

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// rowsDriver serves two fixed rows of columns (name, n).
type rowsDriver struct{}

func (rowsDriver) Open(string) (driver.Conn, error) { return rowsConn{}, nil }

type rowsConn struct{ fakeConn }

func (rowsConn) Prepare(string) (driver.Stmt, error) { return rowsStmt{}, nil }

type rowsStmt struct{ fakeStmt }

func (rowsStmt) Query([]driver.Value) (driver.Rows, error) { return &twoRows{}, nil }

type twoRows struct{ i int }

func (*twoRows) Columns() []string { return []string{"name", "n"} }
func (*twoRows) Close() error      { return nil }
func (r *twoRows) Next(dest []driver.Value) error {
	data := [][]driver.Value{{"ada", int64(1)}, {"bob", int64(2)}}
	if r.i >= len(data) {
		return io.EOF
	}
	copy(dest, data[r.i])
	r.i++
	return nil
}

var registerRowsDriverOnce sync.Once

func openRowsDB(t *testing.T) *sql.DB {
	registerRowsDriverOnce.Do(func() { sql.Register("hqdb-rows-export", rowsDriver{}) })
	db, err := sql.Open("hqdb-rows-export", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestQueryAppliesTransformToEveryFormat(t *testing.T) {
	upper := func(columns []string, row []any) []any {
		out := make([]any, len(row))
		copy(out, row)
		for i, c := range columns {
			if c == "name" {
				out[i] = "X-" + row[i].(string)
			}
		}
		return out
	}
	cases := []struct {
		format string
		want   string
		fn     func(columns []string, row []any) []any
	}{
		{"csv", "name,n\nada,1\nbob,2\n", nil},
		{"csv", "name,n\nX-ada,1\nX-bob,2\n", upper},
		{"json", "[{\"n\":1,\"name\":\"X-ada\"}\n,{\"n\":2,\"name\":\"X-bob\"}\n]\n", upper},
		{"ndjson", "{\"n\":1,\"name\":\"X-ada\"}\n{\"n\":2,\"name\":\"X-bob\"}\n", upper},
		{"ndjson", "{\"n\":1,\"name\":\"ada\"}\n{\"n\":2,\"name\":\"bob\"}\n", nil},
	}
	db := openRowsDB(t)
	for _, c := range cases {
		name := c.format
		if c.fn == nil {
			name += "-nil"
		}
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "out."+c.format)
			n, err := Query(context.Background(), db, "SELECT", dest, c.format, 0, c.fn)
			if err != nil || n != 2 {
				t.Fatalf("n=%d err=%v", n, err)
			}
			got, _ := os.ReadFile(dest)
			if string(got) != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestQueryTransformRunsOncePerRow(t *testing.T) {
	calls := 0
	count := func(columns []string, row []any) []any {
		calls++
		if strings.Join(columns, ",") != "name,n" {
			t.Errorf("columns = %v", columns)
		}
		return row
	}
	dest := filepath.Join(t.TempDir(), "out.json")
	if _, err := Query(context.Background(), openRowsDB(t), "SELECT", dest, "json", 0, count); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("transform calls = %d, want 2", calls)
	}
}
