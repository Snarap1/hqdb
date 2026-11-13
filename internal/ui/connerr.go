package ui

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// openError marks an error from opening a connection (connPool.Open or
// Reopen), as opposed to one from work on an open connection. Its text is
// the underlying error's.
type openError struct{ err error }

func (e *openError) Error() string { return e.err.Error() }
func (e *openError) Unwrap() error { return e.err }

// isConnError reports whether err means the connection itself is gone or
// could not be made, so a retry on a reopened connection can succeed:
// a failed open, a driver's bad-connection error, a network error, an
// unexpected end of stream, a postgres connect error, a mongodb network or
// server selection error, a closed redis client or pool timeout, or a
// deadline (the UI's own contexts have none, so a deadline is a driver's
// connect timeout). Anything else, a syntax error in a WHERE above all,
// is not retried.
func isConnError(err error) bool {
	if err == nil {
		return false
	}
	var oe *openError
	if errors.As(err, &oe) {
		return true
	}
	for _, target := range []error{
		driver.ErrBadConn, io.EOF, io.ErrUnexpectedEOF, net.ErrClosed,
		mysql.ErrInvalidConn, redis.ErrClosed, redis.ErrPoolTimeout,
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EPIPE,
		context.DeadlineExceeded,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		return true
	}
	if mongo.IsNetworkError(err) || errors.As(err, &topology.ServerSelectionError{}) {
		return true
	}
	return false
}

// reconnect is embedded in a background work result message; to is the
// Profile whose connection the work reopened and then succeeded on, so the
// Model can say "reconnected" (see Model.Update), or "" when it did not.
type reconnect struct{ to string }

func (r reconnect) reconnected() string { return r.to }

// reconnectedTo is the reconnect a message carries for profile when ok.
func reconnectedTo(profile string, ok bool) reconnect {
	if !ok {
		return reconnect{}
	}
	return reconnect{to: profile}
}

// reconnecter is a message that may report a reconnect.
type reconnecter interface{ reconnected() string }

// run runs fn on k's cached connection (opening one if needed). When fn
// fails with a connection-class error (isConnError) and ctx is not
// cancelled, it runs fn once more on a reopened connection, so a
// connection the server dropped recovers without the user doing anything;
// any other error is returned as is, since it would fail the same way
// twice. retryable lets fn veto the retry after it has made progress (an
// export that already wrote rows). reconnected reports that the retry ran
// and succeeded. An error from opening is an *openError.
func (p *connPool) run(ctx context.Context, k poolKey, fn func(conn adapters.DB) (retryable bool, err error)) (reconnected bool, err error) {
	conn, err := p.Open(k)
	if err != nil {
		return false, &openError{err}
	}
	retryable, err := fn(conn)
	if err == nil || !retryable || ctx.Err() != nil || !isConnError(err) {
		return false, err
	}
	conn, rerr := p.Reopen(k)
	if rerr != nil {
		return false, &openError{rerr}
	}
	if _, err = fn(conn); err != nil {
		return false, err
	}
	return true, nil
}
