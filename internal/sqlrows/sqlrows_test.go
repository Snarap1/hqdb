package sqlrows

import (
	"context"
	"testing"

	"github.com/Snarap1/hqdb/internal/fakesql"
)

func TestStreamStopsAtTheLimit(t *testing.T) {
	cases := []struct {
		rows, limit   int
		wantN         int
		wantTruncated bool
	}{
		{rows: 10, limit: 0, wantN: 10},
		{rows: 10, limit: 3, wantN: 3, wantTruncated: true},
		{rows: 10, limit: 9, wantN: 9, wantTruncated: true},
		{rows: 10, limit: 10, wantN: 10},
		{rows: 10, limit: 11, wantN: 10},
		{rows: 0, limit: 5, wantN: 0},
	}
	for _, c := range cases {
		db, _ := fakesql.Open(c.rows, 2)
		cur, err := Query(context.Background(), db, "SELECT")
		if err != nil {
			t.Fatal(err)
		}
		var got [][]any
		n, truncated, err := Stream(cur.Rows, []string{"a", "b"}, c.limit, func(row []any) error {
			got = append(got, row)
			return nil
		})
		cur.Close(truncated)
		db.Close()
		if err != nil || n != c.wantN || truncated != c.wantTruncated || len(got) != c.wantN {
			t.Errorf("rows %d limit %d: n = %d, truncated = %v, emitted %d, err = %v; want %d, %v",
				c.rows, c.limit, n, truncated, len(got), err, c.wantN, c.wantTruncated)
		}
		for i, row := range got {
			if row[0] != int64(i) {
				t.Errorf("rows %d limit %d: row %d = %v, want first value %d", c.rows, c.limit, i, row, i)
			}
		}
	}
}

// TestCursorCloseStopsAPoolQueryButDrainsASession pins where an abandoned
// result is stopped on the server and where it is read to the end.
func TestCursorCloseStopsAPoolQueryButDrainsASession(t *testing.T) {
	const total, read = 1000, 5
	cases := []struct {
		name          string
		pinned        bool
		abandoned     bool
		wantDrained   int64
		wantCancelled int64
	}{
		{name: "pool, abandoned", abandoned: true, wantDrained: 0, wantCancelled: 1},
		{name: "pool, not abandoned", abandoned: false, wantDrained: total - read, wantCancelled: 0},
		{name: "pinned session, abandoned", pinned: true, abandoned: true, wantDrained: total - read, wantCancelled: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, st := fakesql.Open(total, 1)
			defer db.Close()
			var q Querier = db
			if c.pinned {
				conn, err := db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				q = conn
			}
			cur, err := Query(context.Background(), q, "SELECT")
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < read && cur.Rows.Next(); i++ {
			}
			cur.Close(c.abandoned)
			if got := st.Drained.Load(); got != c.wantDrained {
				t.Errorf("drained %d rows, want %d", got, c.wantDrained)
			}
			if got := st.Cancelled.Load(); got != c.wantCancelled {
				t.Errorf("cancelled %d queries, want %d", got, c.wantCancelled)
			}
			// The pool replaces a connection the cancellation broke; a
			// pinned session stays usable for its next statement.
			next, err := Query(context.Background(), q, "SELECT")
			if err != nil {
				t.Fatalf("next query: %v", err)
			}
			n, _, err := Stream(next.Rows, []string{"a"}, 0, func([]any) error { return nil })
			next.Close(false)
			if err != nil || n != total {
				t.Errorf("next query read %d rows, err = %v; want %d", n, err, total)
			}
		})
	}
}
