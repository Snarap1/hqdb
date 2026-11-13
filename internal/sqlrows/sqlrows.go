// Package sqlrows scans and streams database/sql result rows as JSON-safe
// values.
package sqlrows

import (
	"context"
	"database/sql"

	"github.com/Snarap1/hqdb/internal/values"
)

// Scan holds the reusable buffer set for scanning one result row.
type Scan struct {
	dest []any
	ptrs []any
}

// NewScan allocates a scan buffer for n columns.
func NewScan(n int) *Scan {
	return &Scan{
		dest: make([]any, n),
		ptrs: make([]any, n),
	}
}

// Row scans the current row and returns the JSON-safe values.
func (s *Scan) Row(rows *sql.Rows) ([]any, error) {
	for i := range s.dest {
		s.ptrs[i] = &s.dest[i]
	}
	if err := rows.Scan(s.ptrs...); err != nil {
		return nil, err
	}
	out := make([]any, len(s.dest))
	for i, v := range s.dest {
		out[i] = values.JSON(v)
	}
	return out, nil
}

// Stream reads rows while handing them to emit, stopping after limit rows
// when limit > 0. truncated reports that it stopped there while at least
// one more row existed.
func Stream(rows *sql.Rows, columns []string, limit int, emit func([]any) error) (n int, truncated bool, err error) {
	scan := NewScan(len(columns))
	for rows.Next() {
		if limit > 0 && n >= limit {
			return n, true, nil
		}
		row, err := scan.Row(rows)
		if err != nil {
			return n, false, err
		}
		if err := emit(row); err != nil {
			return n, false, err
		}
		n++
	}
	return n, false, rows.Err()
}

// Querier is what Query runs a statement on: a pool (*sql.DB) or one
// pinned session (*sql.Conn).
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Cursor is one query's result, which its reader may leave unfinished.
type Cursor struct {
	Rows   *sql.Rows
	cancel context.CancelFunc // nil: the query must not be cancelled
}

// Query runs sqlText on q. On a pool the statement runs under a context of
// its own, so that Close can stop it on the server; on a pinned session it
// runs under ctx itself.
func Query(ctx context.Context, q Querier, sqlText string) (*Cursor, error) {
	if _, pool := q.(*sql.DB); !pool {
		rows, err := q.QueryContext(ctx, sqlText)
		if err != nil {
			return nil, err
		}
		return &Cursor{Rows: rows}, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	rows, err := q.QueryContext(ctx, sqlText)
	if err != nil {
		cancel()
		return nil, err
	}
	return &Cursor{Rows: rows, cancel: cancel}, nil
}

// Close closes the result. abandoned says the reader stopped before the
// end of the result (a row limit, or a failure); the close error is not
// reported, since every row the caller wanted is already read.
//
// Closing an unfinished result makes pgx, go-sql-driver/mysql, and
// clickhouse-go (HTTP and native) read the rest of it to the end: a limit
// of 3 on a result of millions of rows took 4 to 14 seconds. go-mssqldb
// (the mssql and odbc adapters) already stops early on Close. So on a pool
// an abandoned query is cancelled first, and the driver stops the server
// instead of reading on; the query does not keep running there, and no
// adapter reports the cancellation as an error once the rows are read. A
// connection the cancellation broke (pgx and mysql close the socket) is
// discarded by the pool rather than reused.
//
// A pinned session is never cancelled, because the connection is the
// session: losing it would lose its SET values, temporary tables, and open
// transaction. Its unfinished result is read to the end instead, which
// keeps the session usable for the next statement.
func (c *Cursor) Close(abandoned bool) {
	if c.cancel != nil {
		if abandoned {
			c.cancel()
		}
		defer c.cancel()
	}
	_ = c.Rows.Close()
}
