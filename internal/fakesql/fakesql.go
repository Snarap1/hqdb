// Package fakesql is a database/sql driver for tests and benchmarks only:
// it serves a fixed synthetic result without a network.
//
// It behaves like pgx and go-sql-driver/mysql where that matters for
// performance: closing an unfinished result reads every remaining row
// first, unless the query's context was cancelled, in which case the
// connection is broken and the pool discards it. Stats records which of the
// two happened, so a test can tell a drained result from a stopped one.
package fakesql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// Stats counts what the driver did; every field is safe to read while
// queries run.
type Stats struct {
	// Served is the number of rows handed to the caller through Next.
	Served atomic.Int64
	// Drained is the number of rows read by Close to finish a result.
	Drained atomic.Int64
	// Cancelled is the number of results closed after their query's
	// context was cancelled; each one broke its connection.
	Cancelled atomic.Int64
	// Connects is the number of connections opened.
	Connects atomic.Int64
}

// Open returns a pool whose every query yields rows rows of cols columns,
// and the Stats it updates.
func Open(rows, cols int) (*sql.DB, *Stats) {
	st := &Stats{}
	return sql.OpenDB(&connector{rows: rows, cols: cols, stats: st}), st
}

type connector struct {
	rows, cols int
	stats      *Stats
}

func (c *connector) Connect(context.Context) (driver.Conn, error) {
	c.stats.Connects.Add(1)
	return &conn{c: c}, nil
}

func (c *connector) Driver() driver.Driver { return drv{} }

type drv struct{}

func (drv) Open(string) (driver.Conn, error) {
	return nil, errors.New("fakesql: use fakesql.Open")
}

type conn struct {
	c      *connector
	broken atomic.Bool
}

var errBroken = errors.New("fakesql: connection broken by a cancelled query")

func (c *conn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.broken.Load() {
		return nil, errBroken
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return newRows(ctx, c, c.c.rows, c.c.cols), nil
}

func (c *conn) ExecContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	if c.broken.Load() {
		return nil, errBroken
	}
	return driver.RowsAffected(0), ctx.Err()
}

func (c *conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fakesql: prepared statements not supported")
}

func (c *conn) Close() error { return nil }

func (c *conn) Begin() (driver.Tx, error) {
	return nil, errors.New("fakesql: transactions not supported")
}

// ResetSession and IsValid make the pool discard a broken connection, as
// it does for a real driver whose socket a cancellation closed.
func (c *conn) ResetSession(context.Context) error {
	if c.broken.Load() {
		return driver.ErrBadConn
	}
	return nil
}

func (c *conn) IsValid() bool { return !c.broken.Load() }

// strs holds the string values: plain text plus every character CSV
// quoting and JSON escaping treat specially.
var strs = []string{
	"user-1@example.org some text value",
	"comma, inside",
	`quote "q" inside`,
	"new\nline",
	"<b>&amp;</b>",
	"unicode ñ 漢字 😀",
	"",
	" leading space",
	"tab\tsep",
	"line separator",
	`back\slash`,
	"carriage\r\nreturn",
}

var jsonBlobs = [][]byte{
	[]byte(`{"id":1,"tags":["a","b"],"ok":true}`),
	[]byte(`[1,2.5,null,"x"]`),
	[]byte("not json \x00 bytes"),
	[]byte(`"a json string"`),
}

var baseTime = time.Date(2025, 3, 4, 5, 6, 7, 890000000, time.UTC)

// Value is the value of column col in row i, the same on every query.
func Value(i, col int) driver.Value {
	switch col % 7 {
	case 0:
		return int64(i)
	case 1:
		return strs[i%len(strs)]
	case 2:
		return float64(i)*1.25 - 3
	case 3:
		return baseTime.Add(time.Duration(i) * 1500 * time.Millisecond)
	case 4:
		return jsonBlobs[i%len(jsonBlobs)]
	case 5:
		if i%3 == 0 {
			return nil
		}
		return i%2 == 0
	default:
		return strs[(i+5)%len(strs)]
	}
}

type rows struct {
	ctx     context.Context
	c       *conn
	n, cols int
	i       int
	names   []string
}

func newRows(ctx context.Context, c *conn, n, cols int) *rows {
	r := &rows{ctx: ctx, c: c, n: n, cols: cols}
	for i := 0; i < cols; i++ {
		r.names = append(r.names, fmt.Sprintf("column_%d", i))
	}
	return r
}

func (r *rows) Columns() []string { return r.names }

func (r *rows) Next(dest []driver.Value) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.i >= r.n {
		return io.EOF
	}
	for c := range dest {
		dest[c] = Value(r.i, c)
	}
	r.i++
	r.c.c.stats.Served.Add(1)
	return nil
}

// Close finishes the result the way pgx and go-sql-driver/mysql do: it
// reads every remaining row, unless the query's context is cancelled, which
// breaks the connection instead and reports the cancellation.
func (r *rows) Close() error {
	if r.i >= r.n {
		return nil
	}
	if err := r.ctx.Err(); err != nil {
		r.c.broken.Store(true)
		r.c.c.stats.Cancelled.Add(1)
		r.i = r.n
		return err
	}
	dest := make([]driver.Value, r.cols)
	for ; r.i < r.n; r.i++ {
		for c := range dest {
			dest[c] = Value(r.i, c)
		}
		r.c.c.stats.Drained.Add(1)
	}
	return nil
}
