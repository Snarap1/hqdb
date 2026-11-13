// Package query runs one SQL statement, collecting its rows as JSON-safe
// values. It does not screen the statement; the MCP layer screens before
// calling Run (ADR-0003).
package query

import (
	"context"
	"database/sql"

	"github.com/Snarap1/hqdb/internal/sqlrows"
)

// Result is the shared shape of run_query and export_query streaming.
type Result struct {
	Columns   []string
	Rows      [][]any
	RowCount  int
	Truncated bool
}

// Run executes a SQL query, collecting at most limit rows (limit <= 0 means
// unlimited). The caller is responsible for screening sqlText first. ctx is
// passed to the driver so a cancelled or timed-out caller stops the query in
// flight; there is no default timeout. When the limit cuts the result
// short, the rest of it is not read: the query is stopped on the server
// (sqlrows.Cursor.Close), which conn, a pool, allows.
func Run(ctx context.Context, conn *sql.DB, sqlText string, limit int) (_ *Result, err error) {
	cur, err := sqlrows.Query(ctx, conn, sqlText)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	defer func() { cur.Close(res.Truncated || err != nil) }()
	if res.Columns, err = cur.Rows.Columns(); err != nil {
		return nil, err
	}
	res.RowCount, res.Truncated, err = sqlrows.Stream(cur.Rows, res.Columns, limit, func(row []any) error {
		res.Rows = append(res.Rows, row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
