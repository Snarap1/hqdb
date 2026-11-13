package ui

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"

	"github.com/Snarap1/hqdb/internal/adapters"
)

func TestIsConnErrorClassifiesConnectionFailuresOnly(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("query failed: %w", err) }
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"bad conn", driver.ErrBadConn, true},
		{"wrapped bad conn", wrap(driver.ErrBadConn), true},
		{"eof", io.EOF, true},
		{"unexpected eof", wrap(io.ErrUnexpectedEOF), true},
		{"net op error", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, true},
		{"connection reset", wrap(syscall.ECONNRESET), true},
		{"closed network connection", wrap(net.ErrClosed), true},
		{"pgconn connect error", &pgconn.ConnectError{}, true},
		{"mysql invalid conn", wrap(mysql.ErrInvalidConn), true},
		{"redis closed", redis.ErrClosed, true},
		{"redis pool timeout", wrap(redis.ErrPoolTimeout), true},
		{"mongo server selection", topology.ServerSelectionError{Wrapped: errors.New("no reachable servers")}, true},
		{"connect deadline", wrap(context.DeadlineExceeded), true},
		{"open failure", &openError{errors.New("unknown profile")}, true},
		{"syntax error", &pgconn.PgError{Code: "42601", Message: `syntax error at or near "WHERE"`}, false},
		{"plain error", errors.New("column \"nope\" does not exist"), false},
		{"cancelled", context.Canceled, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isConnError(tt.err); got != tt.want {
				t.Errorf("isConnError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestConnPoolRunRetriesOnlyConnectionErrors(t *testing.T) {
	syntax := errors.New(`syntax error at or near "WHERE"`)
	tests := []struct {
		name            string
		first           error
		retryable       bool
		wantCalls       int
		wantErr         error
		wantReconnected bool
	}{
		{"success", nil, true, 1, nil, false},
		{"syntax error is not retried", syntax, true, 1, syntax, false},
		{"bad conn is retried", driver.ErrBadConn, true, 2, nil, true},
		{"progress vetoes the retry", driver.ErrBadConn, false, 1, driver.ErrBadConn, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opener, _ := countingOpener(nil)
			p := newTestPool(opener)
			calls := 0
			re, err := p.run(context.Background(), poolKey{profile: "pg-dev"}, func(adapters.DB) (bool, error) {
				calls++
				if calls == 1 {
					return tt.retryable, tt.first
				}
				return true, nil
			})
			if calls != tt.wantCalls {
				t.Errorf("fn called %d times, want %d", calls, tt.wantCalls)
			}
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if re != tt.wantReconnected {
				t.Errorf("reconnected = %v, want %v", re, tt.wantReconnected)
			}
		})
	}
}

func TestConnPoolRunReportsAnOpenFailureAsConnectionClass(t *testing.T) {
	want := errors.New("dial tcp: refused")
	p := newTestPool(func(string, string) (adapters.DB, error) { return nil, want })
	_, err := p.run(context.Background(), poolKey{profile: "pg-dev"}, func(adapters.DB) (bool, error) { return true, nil })
	if !errors.Is(err, want) || !isConnError(err) || err.Error() != want.Error() {
		t.Errorf("err = %v, want the open error, connection-class, with its own text", err)
	}
}
