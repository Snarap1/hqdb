package console

import (
	"context"
	"testing"

	"github.com/Snarap1/hqdb/internal/fakesql"
)

// TestRunPreviewLimitKeepsTheSession pins the Console's choice: a result
// cut short by the limit is read to the end, never cancelled, so the
// pinned session survives for the next statement.
func TestRunPreviewLimitKeepsTheSession(t *testing.T) {
	cases := []struct {
		rows, limit   int
		wantRows      int
		wantTruncated bool
		wantDrained   int64
	}{
		{rows: 1000, limit: 10, wantRows: 10, wantTruncated: true, wantDrained: 1000 - 11},
		{rows: 10, limit: 10, wantRows: 10},
		{rows: 3, limit: 10, wantRows: 3},
	}
	for _, c := range cases {
		db, st := fakesql.Open(c.rows, 2)
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		res, err := Run(context.Background(), conn, "SELECT a, b FROM t", c.limit)
		if err != nil {
			t.Fatalf("rows %d limit %d: %v", c.rows, c.limit, err)
		}
		if len(res.Rows) != c.wantRows || res.Truncated != c.wantTruncated {
			t.Errorf("rows %d limit %d: rows = %d, truncated = %v; want %d, %v",
				c.rows, c.limit, len(res.Rows), res.Truncated, c.wantRows, c.wantTruncated)
		}
		if got := st.Drained.Load(); got != c.wantDrained {
			t.Errorf("rows %d limit %d: drained %d rows, want %d", c.rows, c.limit, got, c.wantDrained)
		}
		if got := st.Cancelled.Load(); got != 0 {
			t.Errorf("rows %d limit %d: cancelled %d queries on the pinned session, want 0", c.rows, c.limit, got)
		}
		if _, err := Run(context.Background(), conn, "SELECT a, b FROM t", c.limit); err != nil {
			t.Errorf("rows %d limit %d: the session is unusable after a truncated result: %v", c.rows, c.limit, err)
		}
		conn.Close()
		db.Close()
	}
}
