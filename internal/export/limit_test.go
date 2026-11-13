package export

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Snarap1/hqdb/internal/fakesql"
)

// TestQueryLimit checks that a limit writes exactly the first rows without
// an error, stops a pool's query instead of draining it, and reads a
// pinned session's result to the end so the session survives.
func TestQueryLimit(t *testing.T) {
	const total = 1000
	cases := []struct {
		pinned        bool
		limit         int
		wantRows      int
		wantDrained   int64
		wantCancelled int64
	}{
		{limit: 7, wantRows: 7, wantCancelled: 1},
		{limit: total, wantRows: total},
		{limit: 0, wantRows: total},
		{pinned: true, limit: 7, wantRows: 7, wantDrained: total - 8},
		{pinned: true, limit: 0, wantRows: total},
	}
	dir := t.TempDir()
	for _, format := range Formats {
		for _, c := range cases {
			db, st := fakesql.Open(total, 3)
			var q Querier = db
			var conn interface{ Close() error }
			if c.pinned {
				pinned, err := db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				q, conn = pinned, pinned
			}
			dest := filepath.Join(dir, "out."+format)
			n, err := Query(context.Background(), q, "SELECT", dest, format, c.limit, nil)
			if err != nil || n != c.wantRows {
				t.Errorf("%s %+v: n = %d, err = %v; want %d and no error", format, c, n, err, c.wantRows)
			}
			if got := st.Drained.Load(); got != c.wantDrained {
				t.Errorf("%s %+v: drained %d rows, want %d", format, c, got, c.wantDrained)
			}
			if got := st.Cancelled.Load(); got != c.wantCancelled {
				t.Errorf("%s %+v: cancelled %d queries, want %d", format, c, got, c.wantCancelled)
			}
			data, _ := os.ReadFile(dest)
			if want := exportOf(t, dir, format, c.wantRows); !bytes.Equal(data, want) {
				t.Errorf("%s %+v: wrote %d bytes, want the %d bytes of the first %d rows", format, c, len(data), len(want), c.wantRows)
			}
			if _, err := Query(context.Background(), q, "SELECT", dest, format, 0, nil); err != nil {
				t.Errorf("%s %+v: the next export failed: %v", format, c, err)
			}
			if conn != nil {
				conn.Close()
			}
			db.Close()
		}
	}
}

// exportOf is the unlimited export of a result of n rows: the fake
// driver's rows are the same on every query, so it is also exactly what a
// limit of n must write from a longer result.
func exportOf(t *testing.T, dir, format string, n int) []byte {
	t.Helper()
	db, _ := fakesql.Open(n, 3)
	defer db.Close()
	dest := filepath.Join(dir, "want."+format)
	if _, err := Query(context.Background(), db, "SELECT", dest, format, 0, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
