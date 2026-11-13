package console

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Snarap1/hqdb/internal/sqlrows"
)

// PreviewLimit is how many rows a Console result shows (spec: "the
// Console preview limit" of 500, a constant in v1).
const PreviewLimit = 500

// Session is the one pinned database session a Console runs every
// statement on; *sql.Conn satisfies it. Statements run in autocommit mode,
// since nothing here ever opens a transaction.
type Session interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Result is one statement's outcome: rows (HasRows) as JSON-safe values,
// at most the limit given to Run with Truncated set when more existed, or
// else the affected row count (Affected is -1 when the driver does not
// report one).
type Result struct {
	HasRows   bool
	Columns   []string
	Rows      [][]any
	Truncated bool
	Affected  int64
	Duration  time.Duration
}

// Run executes stmt on s as typed, without screening it (ADR-0003). A
// statement that returns rows is read with Query, collecting at most limit
// rows; anything else runs with Exec so its affected row count is known.
func Run(ctx context.Context, s Session, stmt string, limit int) (*Result, error) {
	start := time.Now()
	if !returnsRows(stmt) {
		res, err := s.ExecContext(ctx, stmt)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			n = -1
		}
		return &Result{Affected: n, Duration: time.Since(start)}, nil
	}
	// The result is read to the end on Close even when the preview limit
	// cuts it short, which costs the time to receive the rest. Stopping the
	// query instead (cancelling its context, as query.Run does on a pool)
	// would break the connection with pgx and go-sql-driver/mysql, and the
	// connection is the Console's session: its SET values, temporary
	// tables, and open transaction would be lost. go-mssqldb stops early on
	// Close by itself; for the others no safe way to discard the rest of a
	// result while keeping the session exists, so the Console keeps the
	// drain; see sqlrows.Cursor.Close.
	rows, err := s.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := &Result{HasRows: len(columns) > 0, Columns: columns, Affected: -1}
	scan := sqlrows.NewScan(len(columns))
	for rows.Next() {
		if len(out.Rows) >= limit {
			out.Truncated = true
			break
		}
		row, err := scan.Row(rows)
		if err != nil {
			return nil, err
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Duration = time.Since(start)
	return out, nil
}

// rowKeywords are the leading keywords of statements that return rows.
var rowKeywords = map[string]bool{
	"select": true, "with": true, "show": true, "explain": true, "describe": true,
	"desc": true, "values": true, "table": true, "pragma": true, "exec": true,
	"execute": true, "call": true,
}

// returnsRows reports whether stmt is expected to return rows: its first
// keyword is a querying one, or it carries a RETURNING or OUTPUT clause
// (postgres / mssql writes that return rows). Strings and comments are
// ignored.
func returnsRows(stmt string) bool {
	words := strings.FieldsFunc(strings.ToLower(codeOnly(stmt)), func(r rune) bool {
		return !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	if len(words) == 0 {
		return false
	}
	if rowKeywords[words[0]] {
		return true
	}
	for _, w := range words[1:] {
		if w == "returning" || w == "output" {
			return true
		}
	}
	return false
}
