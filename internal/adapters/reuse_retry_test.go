package adapters

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
)

// The pooled connections rely on one database/sql contract (ADR-0006): a
// reset that returns driver.ErrBadConn discards the connection and runs the
// statement on another one, while a statement that fails once sent is
// never run again. These tests pin that contract with a fake driver whose
// reset is resetPostgres.

// retryDriver records every statement that reached a connection.
type retryDriver struct {
	mu       sync.Mutex
	epoch    *sessionEpoch
	conns    []*retryConn
	sent     []string
	discards int
	resetErr error // what the next reset's DISCARD ALL returns
	execErr  error // what an executed statement returns
}

func (d *retryDriver) Connect(context.Context) (driver.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := &retryConn{d: d, custom: map[string]any{}, tx: 'I'}
	d.epoch.mark(c.custom) // what AfterConnect does
	d.conns = append(d.conns, c)
	return c, nil
}

func (d *retryDriver) Driver() driver.Driver { return nil }

type retryConn struct {
	d      *retryDriver
	custom map[string]any
	tx     byte
	closed bool
}

func (c *retryConn) data() map[string]any { return c.custom }
func (c *retryConn) txStatus() byte       { return c.tx }
func (c *retryConn) discardAll(context.Context) error {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	c.d.discards++
	err := c.d.resetErr
	c.d.resetErr = nil
	return err
}

func (c *retryConn) ResetSession(ctx context.Context) error { return resetPostgres(ctx, c.d.epoch, c) }

func (c *retryConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	c.d.sent = append(c.d.sent, query)
	if query == "BEGIN" {
		c.tx = 'T'
	}
	if c.d.execErr != nil {
		return nil, c.d.execErr
	}
	return driver.RowsAffected(1), nil
}

func (c *retryConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (c *retryConn) Close() error                        { c.closed = true; return nil }
func (c *retryConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }

func (d *retryDriver) connCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.conns)
}

func TestResetSessionResetsIdleConnectionsNow(t *testing.T) {
	cases := []struct {
		name         string
		inTx         bool
		wantDiscards int
		wantClosed   bool
	}{
		{"idle connection runs DISCARD ALL when the call ends", false, 1, false},
		{"idle connection inside a transaction is closed", true, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &retryDriver{epoch: &sessionEpoch{}}
			db := &SQL{kind: "postgres", db: sql.OpenDB(d), epoch: d.epoch}
			defer db.Close()
			ctx := context.Background()
			if _, err := db.db.ExecContext(ctx, "SET search_path TO x"); err != nil {
				t.Fatal(err)
			}
			d.conns[0].tx = map[bool]byte{false: 'I', true: 'T'}[c.inTx]
			if err := ResetSession(ctx, db); err != nil {
				t.Fatal(err)
			}
			if d.discards != c.wantDiscards || d.conns[0].closed != c.wantClosed {
				t.Errorf("discards = %d, closed = %v; want %d, %v", d.discards, d.conns[0].closed, c.wantDiscards, c.wantClosed)
			}
			// The next call finds the connection clean: no second reset.
			if _, err := db.db.ExecContext(ctx, "SELECT 1"); err != nil {
				t.Fatal(err)
			}
			if d.discards != c.wantDiscards {
				t.Errorf("reset ran again on the next call: discards = %d", d.discards)
			}
		})
	}
}

func TestReusedConnectionRetry(t *testing.T) {
	cases := []struct {
		name string
		// setup runs between the first call and the second checkout.
		setup     func(d *retryDriver)
		wantErr   bool
		wantSent  []string
		wantConns int
	}{
		{
			name:      "healthy connection is reused",
			setup:     func(*retryDriver) {},
			wantSent:  []string{"first", "second"},
			wantConns: 1,
		},
		{
			name:      "failed reset reconnects and runs the statement once",
			setup:     func(d *retryDriver) { d.resetErr = io.EOF },
			wantSent:  []string{"first", "second"},
			wantConns: 2,
		},
		{
			name:      "open transaction reconnects and runs the statement once",
			setup:     func(d *retryDriver) { d.conns[0].tx = 'T' },
			wantSent:  []string{"first", "second"},
			wantConns: 2,
		},
		{
			name:      "statement that fails once sent is not run again",
			setup:     func(d *retryDriver) { d.execErr = io.ErrUnexpectedEOF },
			wantErr:   true,
			wantSent:  []string{"first", "second"},
			wantConns: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &retryDriver{epoch: &sessionEpoch{}}
			db := sql.OpenDB(d)
			defer db.Close()
			ctx := context.Background()
			if _, err := db.ExecContext(ctx, "first"); err != nil {
				t.Fatal(err)
			}
			c.setup(d)
			d.epoch.advance() // as ResetSession does when the call ends
			_, err := db.ExecContext(ctx, "second")
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got := d.sent; len(got) != len(c.wantSent) || got[0] != c.wantSent[0] || got[1] != c.wantSent[1] {
				t.Errorf("statements sent = %q, want %q", got, c.wantSent)
			}
			if got := d.connCount(); got != c.wantConns {
				t.Errorf("connections = %d, want %d", got, c.wantConns)
			}
		})
	}
}
