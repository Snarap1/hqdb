package export

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/Snarap1/hqdb/internal/mask"
)

// benchRows is the number of synthetic rows one benchmark export writes.
const benchRows = 2000

// benchDriver serves benchRows synthetic rows for the statement "narrow"
// or "wide", so export.Query runs end to end without a database.
type benchDriver struct{}
type benchConn struct{}
type benchResult struct {
	wide bool
	i    int
}

func (benchDriver) Open(string) (driver.Conn, error)  { return benchConn{}, nil }
func (benchConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepare") }
func (benchConn) Close() error                        { return nil }
func (benchConn) Begin() (driver.Tx, error)           { return nil, errors.New("no tx") }
func (benchConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	return &benchResult{wide: q == "wide"}, nil
}

func (r *benchResult) Columns() []string {
	if r.wide {
		return benchWideCols
	}
	return benchNarrowCols
}

func (r *benchResult) Close() error { return nil }

func (r *benchResult) Next(dest []driver.Value) error {
	if r.i >= benchRows {
		return io.EOF
	}
	if r.wide {
		copy(dest, benchWideRow(r.i))
	} else {
		copy(dest, []driver.Value{int64(100000 + r.i), "active", 129.9, "2024-03-15T10:22:01Z", "SKU-" + strconv.Itoa(r.i)})
	}
	r.i++
	return nil
}

var benchNarrowCols = []string{"id", "status", "amount", "created_at", "sku"}

var benchWideCols = []string{
	"id", "user_id", "first_name", "last_name", "email", "phone", "birth_date", "status", "plan",
	"amount", "currency", "created_at", "deleted_at", "product_name", "description",
	"tags", "metadata", "ip_address", "country", "score", "is_active", "external_ref", "notes", "comment_text",
}

const benchCleanText = "Order shipped to the warehouse on Monday. The customer asked to change the delivery window " +
	"and to leave the parcel at the reception. Item count 3, total 129.90 EUR, status confirmed."

const benchDirtyText = "Contact ivan.petrov@example.com or call +7 916 123-45-67, card 4111 1111 1111 1111, " +
	"from 192.168.1.10, INN 7707083893."

func benchWideRow(i int) []driver.Value {
	notes := benchCleanText
	if i%10 == 0 { // one row in ten has personal data in free text
		notes = benchDirtyText
	}
	return []driver.Value{
		int64(i), int64(500000 + i), "Ivan", "Petrov", "ivan" + strconv.Itoa(i) + "@example.com",
		"+7 916 123-" + strconv.Itoa(10+i%90) + "-67", "1985-06-15", "active", "pro", 129.9, "EUR",
		"2024-03-15T10:22:01Z", nil, "Blue cotton T-shirt", benchCleanText, `["new","vip","eu"]`,
		`{"source":"web","campaign":"spring","n":3}`, "192.168.1.10", "DE", 0.75, true,
		"3f2504e0-4f89-11d3-9a0c-0305e82c3301", notes, "Please deliver after 5pm, thanks.",
	}
}

var registerBenchDriverOnce sync.Once

// BenchmarkExport compares masked and unmasked exports of the same rows.
func BenchmarkExport(b *testing.B) {
	registerBenchDriverOnce.Do(func() { sql.Register("hqdb-bench-export", benchDriver{}) })
	db, err := sql.Open("hqdb-bench-export", "")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	for _, shape := range []string{"narrow", "wide"} {
		for _, format := range []string{"ndjson", "csv"} {
			for _, masked := range []bool{false, true} {
				b.Run(fmt.Sprintf("%s/%s/masked=%v", shape, format, masked), func(b *testing.B) {
					var transform RowTransform
					if masked {
						m, err := mask.New(nil, []byte("0123456789abcdef0123456789abcdef"))
						if err != nil {
							b.Fatal(err)
						}
						transform = m.Row
					}
					dest := filepath.Join(b.TempDir(), "out."+format)
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						n, err := Query(context.Background(), db, shape, dest, format, 0, transform)
						if err != nil || n != benchRows {
							b.Fatalf("n=%d err=%v", n, err)
						}
					}
					b.ReportMetric(float64(benchRows*b.N)/b.Elapsed().Seconds(), "rows/s")
				})
			}
		}
	}
}
