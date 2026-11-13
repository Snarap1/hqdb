package export

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Snarap1/hqdb/internal/fakesql"
)

// BenchmarkQuery measures Query per format on a fake driver, so the cost is
// the writers' and the file writes', not a network's.
func BenchmarkQuery(b *testing.B) {
	for _, format := range Formats {
		for _, n := range []int{1000, 10000} {
			b.Run(fmt.Sprintf("%s/rows=%d", format, n), func(b *testing.B) {
				db, _ := fakesql.Open(n, 8)
				defer db.Close()
				dest := filepath.Join(b.TempDir(), "out."+format)
				b.ReportAllocs()
				for b.Loop() {
					if _, err := Query(context.Background(), db, "SELECT", dest, format, 0, nil); err != nil {
						b.Fatal(err)
					}
				}
				if st, err := os.Stat(dest); err == nil {
					b.ReportMetric(float64(st.Size())/1e6, "MB")
				}
			})
		}
	}
}

// BenchmarkDocuments measures Documents per format.
func BenchmarkDocuments(b *testing.B) {
	for _, format := range Formats {
		b.Run(fmt.Sprintf("%s/docs=10000", format), func(b *testing.B) {
			dest := filepath.Join(b.TempDir(), "docs."+format)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Documents(dest, format, fixedDocuments(10000)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
