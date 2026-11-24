package adapters

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// OpenReusable opens a connection that the MCP server keeps across tool
// calls (ADR-0006). It behaves like OpenDatabase, with two differences:
//
//   - a postgres connection can reset the session state of its physical
//     connections (DISCARD ALL, see ResetSession), and discards one that is
//     inside a transaction instead;
//   - a redis client never retries a command, so a command whose reply was
//     lost is not sent twice.
//
// Every other adapter opens exactly as OpenDatabase does; ResetsSession
// tells the caller whether it may reuse the connection after a statement
// it did not write itself.
func OpenReusable(ctx context.Context, p map[string]any, database string) (DB, error) {
	q, adapter, err := resolved(ctx, p)
	if err != nil {
		return nil, err
	}
	switch adapter {
	case "postgres":
		if database != "" {
			if err := checkPostgresDatabase(ctx, q, database); err != nil {
				return nil, err
			}
		}
		return connectReusablePostgres(q, database)
	case "redis":
		return openRedisWith(q, true)
	}
	return OpenDatabase(ctx, q, database)
}

// ResetSession resets, right away, the session state of every idle
// physical connection of db, a connection opened by OpenReusable for which
// ResetsSession holds. The MCP server calls it when a tool call that ran a
// statement hqdb did not write ends, so a lock or an open transaction is
// released when the call ends rather than when the connection is next
// used, as closing the connection used to do.
//
//   - postgres: each connection runs DISCARD ALL, or is closed when it is
//     inside a transaction block (see resetPostgres);
//   - mssql and odbc: each connection sends one trivial request, which
//     carries the TDS reset-connection flag go-mssqldb sets on reuse;
//   - clickhouse over HTTP and mongodb: nothing to reset.
//
// A connection whose reset fails is closed by database/sql. The caller
// must have the connection to itself: ResetSession checks out every idle
// physical connection at once, and a concurrent user would make it dial.
func ResetSession(ctx context.Context, db DB) error {
	c, ok := db.(*SQL)
	if !ok {
		return nil
	}
	var probe string
	switch {
	case c.kind == "postgres" && c.epoch != nil:
		// Every idle connection is now stale; acquiring it runs the reset.
		c.epoch.advance()
	case c.kind == "mssql" || c.kind == "odbc":
		probe = "SELECT 1"
	default:
		return nil
	}
	idle := c.db.Stats().Idle
	conns := make([]*sql.Conn, 0, idle)
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()
	for range idle {
		conn, err := c.db.Conn(ctx)
		if err != nil {
			return err
		}
		conns = append(conns, conn)
		if probe != "" {
			if _, err := conn.ExecContext(ctx, probe); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResetsSession reports whether db leaves no session state behind for the
// next checkout whatever statements ran on it (ADR-0006):
//
//   - postgres opened by OpenReusable: DISCARD ALL before reuse;
//   - mssql and odbc: go-mssqldb sets the TDS reset-connection flag on the
//     first request of a reused connection, which also rolls back an open
//     transaction;
//   - clickhouse over HTTP: every request is its own session;
//   - mongodb: operations carry no session state between calls.
//
// mysql (go-sql-driver has no COM_RESET_CONNECTION), clickhouse over the
// native protocol (no reset command), and redis (no reset that keeps
// authentication and the selected database) report false: they may be
// reused only after statements known not to change session state.
func ResetsSession(db DB) bool {
	switch c := db.(type) {
	case *SQL:
		switch c.kind {
		case "postgres":
			return c.epoch != nil
		case "mssql", "odbc":
			return true
		case "clickhouse":
			return c.stateless
		}
		return false
	case *Mongo:
		return true
	}
	return false
}

// sessionEpoch numbers the resets of one reusable postgres connection
// (ResetSession advances it). Each physical connection records the epoch
// it was last reset for (in its pgconn CustomData), so a connection that
// may carry state from before the last advance is reset once before it is
// used again, and one already reset is not. Within one tool call the
// epoch stays put, so connections reused inside it are never reset.
type sessionEpoch struct {
	n atomic.Uint64
}

// epochKey is the pgconn CustomData key holding a connection's epoch.
const epochKey = "hqdb.session_epoch"

func (e *sessionEpoch) advance() { e.n.Add(1) }

// stale reports whether a physical connection with custom data needs a
// reset before it is used again.
func (e *sessionEpoch) stale(data map[string]any) bool {
	seen, ok := data[epochKey].(uint64)
	return !ok || seen != e.n.Load()
}

// mark records that a physical connection is clean for the current epoch.
func (e *sessionEpoch) mark(data map[string]any) { data[epochKey] = e.n.Load() }

// pgSession is the part of a pgx connection the reset needs; *pgx.Conn
// satisfies it through pgxSession, and tests use a fake.
type pgSession interface {
	data() map[string]any
	txStatus() byte
	discardAll(ctx context.Context) error
}

type pgxSession struct{ c *pgx.Conn }

func (s pgxSession) data() map[string]any { return s.c.PgConn().CustomData() }
func (s pgxSession) txStatus() byte       { return s.c.PgConn().TxStatus() }

// discardAll runs DISCARD ALL and then clears pgx's statement caches:
// DISCARD ALL deallocates the prepared statements pgx has cached, so the
// caches would otherwise name statements the server no longer has.
// DeallocateAll is the public way to clear them; its own DEALLOCATE ALL is
// then a no-op on the server.
func (s pgxSession) discardAll(ctx context.Context) error {
	if _, err := s.c.PgConn().Exec(ctx, "DISCARD ALL").ReadAll(); err != nil {
		return err
	}
	return s.c.DeallocateAll(ctx)
}

// resetPostgres is the reset a reusable postgres connection runs before a
// physical connection serves a statement again. A connection still inside
// a transaction block (an unfinished BEGIN, or a failed one) is discarded:
// DISCARD ALL cannot run there, and closing it is what rolls it back. Any
// failure discards the connection too: driver.ErrBadConn tells
// database/sql to close it and run the statement on another connection,
// which is safe because the statement has not been sent yet.
func resetPostgres(ctx context.Context, e *sessionEpoch, s pgSession) error {
	data := s.data()
	if !e.stale(data) {
		return nil
	}
	if s.txStatus() != 'I' {
		return driver.ErrBadConn
	}
	if err := s.discardAll(ctx); err != nil {
		return driver.ErrBadConn
	}
	e.mark(data)
	return nil
}

// connectReusablePostgres opens a postgres connection whose physical
// connections are reset after each epoch advance (see ResetSession).
func connectReusablePostgres(p map[string]any, database string) (*SQL, error) {
	dsn, err := postgresDSN(p, database)
	if err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	epoch := &sessionEpoch{}
	db := stdlib.OpenDB(*cfg,
		// A new physical connection starts clean for the current epoch.
		stdlib.OptionAfterConnect(func(_ context.Context, c *pgx.Conn) error {
			epoch.mark(c.PgConn().CustomData())
			return nil
		}),
		stdlib.OptionResetSession(func(ctx context.Context, c *pgx.Conn) error {
			return resetPostgres(ctx, epoch, pgxSession{c})
		}),
		// DISCARD ALL already proves a stale connection alive, so the
		// liveness ping is kept only for connections that need no reset,
		// with pgx's default of an idle time over a second.
		stdlib.OptionShouldPing(func(_ context.Context, sp stdlib.ShouldPingParams) bool {
			return !epoch.stale(sp.Conn.PgConn().CustomData()) && sp.IdleDuration > time.Second
		}),
	)
	return &SQL{kind: "postgres", db: db, profile: p, database: database, epoch: epoch}, nil
}
