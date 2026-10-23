package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
)

// fakeDriver is a minimal database/sql driver used to prove that Run reaches
// real execution instead of refusing a statement on its own; screening is
// the MCP layer's job now (ADR-0003).
type fakeDriver struct{}

func (fakeDriver) Open(name string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(query string) (driver.Stmt, error) { return fakeStmt{}, nil }
func (fakeConn) Close() error                              { return nil }
func (fakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fakeConn: transactions not supported")
}

type fakeStmt struct{}

func (fakeStmt) Close() error  { return nil }
func (fakeStmt) NumInput() int { return -1 }
func (fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (fakeStmt) Query(args []driver.Value) (driver.Rows, error) { return fakeRows{}, nil }

// fakeRows reports one column and no rows.
type fakeRows struct{}

func (fakeRows) Columns() []string              { return []string{"n"} }
func (fakeRows) Close() error                   { return nil }
func (fakeRows) Next(dest []driver.Value) error { return io.EOF }

var registerFakeDriverOnce sync.Once

func openFakeDB(t *testing.T) *sql.DB {
	registerFakeDriverOnce.Do(func() { sql.Register("hqdb-fake-query", fakeDriver{}) })
	db, err := sql.Open("hqdb-fake-query", "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRunNoLongerScreens(t *testing.T) {
	// A statement that the old readonly check would have refused must now
	// reach the driver instead of being rejected inside Run.
	db := openFakeDB(t)
	res, err := Run(context.Background(), db, "DELETE FROM widgets", 10)
	if err != nil {
		t.Fatalf("Run returned an error instead of executing: %v", err)
	}
	if res.RowCount != 0 {
		t.Errorf("RowCount = %d, want 0", res.RowCount)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "n" {
		t.Errorf("Columns = %v, want [n]", res.Columns)
	}
}

func TestRunRespectsCancelledContext(t *testing.T) {
	// A context cancelled before the call must stop Run from ever reaching
	// the driver, proving ctx is threaded through to the connection and not
	// substituted with context.Background().
	db := openFakeDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, db, "SELECT 1", 10)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want a context.Canceled error", err)
	}
}
