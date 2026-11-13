package query

import (
	"context"
	"fmt"
	"testing"

	"github.com/Snarap1/hqdb/internal/fakesql"
	"github.com/Snarap1/hqdb/internal/sqlrows"
)

// BenchmarkRunLimit measures a limit of 50 on a long result. "run" is Run
// on a pool, which stops the query at the limit; "drain" reads the same 50
// rows on a pinned session, whose Close reads the rest of the result (the
// cost Run paid before it stopped queries, and the SQL Console still pays).
// The fake driver's rows are cheap, so on a real server the gap is wider:
// the rest has to cross the network too.
func BenchmarkRunLimit(b *testing.B) {
	for _, total := range []int{10000, 200000} {
		db, _ := fakesql.Open(total, 8)
		b.Run(fmt.Sprintf("run/rows=%d", total), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				res, err := Run(context.Background(), db, "SELECT", 50)
				if err != nil || !res.Truncated {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("drain/rows=%d", total), func(b *testing.B) {
			conn, err := db.Conn(context.Background())
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()
			b.ReportAllocs()
			for b.Loop() {
				cur, err := sqlrows.Query(context.Background(), conn, "SELECT")
				if err != nil {
					b.Fatal(err)
				}
				cols, _ := cur.Rows.Columns()
				_, truncated, err := sqlrows.Stream(cur.Rows, cols, 50, func([]any) error { return nil })
				cur.Close(truncated)
				if err != nil || !truncated {
					b.Fatal(err)
				}
			}
		})
		db.Close()
	}
}

// BenchmarkRunAll measures reading a whole result, the path the limit
// change must not slow down.
func BenchmarkRunAll(b *testing.B) {
	db, _ := fakesql.Open(10000, 8)
	defer db.Close()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run(context.Background(), db, "SELECT", 0); err != nil {
			b.Fatal(err)
		}
	}
}
