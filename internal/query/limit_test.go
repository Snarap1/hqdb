package query

import (
	"context"
	"reflect"
	"testing"

	"github.com/Snarap1/hqdb/internal/fakesql"
	"github.com/Snarap1/hqdb/internal/values"
)

// TestRunLimit checks the row limit end to end on a driver that, like pgx
// and go-sql-driver/mysql, reads the rest of an unfinished result on
// Close: a limit that cuts the result short returns the first rows,
// Truncated, and no error, and stops the query instead of draining it.
func TestRunLimit(t *testing.T) {
	const cols = 7
	cases := []struct {
		rows, limit   int
		wantRows      int
		wantTruncated bool
		wantCancelled int64
	}{
		{rows: 1000, limit: 50, wantRows: 50, wantTruncated: true, wantCancelled: 1},
		{rows: 1000, limit: 999, wantRows: 999, wantTruncated: true, wantCancelled: 1},
		{rows: 1000, limit: 1000, wantRows: 1000},
		{rows: 1000, limit: 5000, wantRows: 1000},
		{rows: 1000, limit: 0, wantRows: 1000},
		{rows: 0, limit: 50, wantRows: 0},
	}
	for _, c := range cases {
		db, st := fakesql.Open(c.rows, cols)
		res, err := Run(context.Background(), db, "SELECT", c.limit)
		if err != nil {
			t.Fatalf("rows %d limit %d: %v", c.rows, c.limit, err)
		}
		if res.RowCount != c.wantRows || len(res.Rows) != c.wantRows || res.Truncated != c.wantTruncated {
			t.Errorf("rows %d limit %d: row_count = %d, rows = %d, truncated = %v; want %d, %v",
				c.rows, c.limit, res.RowCount, len(res.Rows), res.Truncated, c.wantRows, c.wantTruncated)
		}
		if len(res.Columns) != cols {
			t.Errorf("rows %d limit %d: columns = %v", c.rows, c.limit, res.Columns)
		}
		for i, row := range res.Rows {
			want := make([]any, cols)
			for col := range want {
				want[col] = values.JSON(fakesql.Value(i, col))
			}
			if !reflect.DeepEqual(row, want) {
				t.Fatalf("rows %d limit %d: row %d = %#v, want %#v", c.rows, c.limit, i, row, want)
			}
		}
		if got := st.Drained.Load(); got != 0 {
			t.Errorf("rows %d limit %d: Close drained %d rows, want 0", c.rows, c.limit, got)
		}
		if got := st.Cancelled.Load(); got != c.wantCancelled {
			t.Errorf("rows %d limit %d: cancelled %d queries, want %d", c.rows, c.limit, got, c.wantCancelled)
		}
		// The pool replaces the connection a stopped query broke.
		if res, err := Run(context.Background(), db, "SELECT", 0); err != nil || res.RowCount != c.rows {
			t.Errorf("rows %d limit %d: the next Run on the pool failed: %v", c.rows, c.limit, err)
		}
		db.Close()
	}
}
