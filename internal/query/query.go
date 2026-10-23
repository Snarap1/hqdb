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
// flight; there is no default timeout.
func Run(ctx context.Context, conn *sql.DB, sqlText string, limit int) (*Result, error) {
	rows, err := conn.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &Result{Columns: columns}
	scan := sqlrows.NewScan(len(columns))
	for rows.Next() {
		if limit > 0 && res.RowCount >= limit {
			res.Truncated = true
			break
		}
		row, err := scan.Row(rows)
		if err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, row)
		res.RowCount++
	}
	return res, rows.Err()
}
