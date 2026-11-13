package adapters

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

// fakePgSession is one physical postgres connection for resetPostgres.
type fakePgSession struct {
	custom   map[string]any
	tx       byte
	fail     error
	discards int
}

func (f *fakePgSession) data() map[string]any { return f.custom }
func (f *fakePgSession) txStatus() byte       { return f.tx }
func (f *fakePgSession) discardAll(context.Context) error {
	f.discards++
	return f.fail
}

func TestResetPostgres(t *testing.T) {
	cases := []struct {
		name string
		// fresh marks the connection clean for the current checkout, as a
		// connection dialed during it is.
		fresh        bool
		tx           byte
		fail         error
		wantErr      error
		wantDiscards int
	}{
		{"connection from an earlier checkout is discarded-all", false, 'I', nil, nil, 1},
		{"connection reused within the checkout is not reset again", true, 'I', nil, nil, 0},
		{"open transaction discards the connection", false, 'T', nil, driver.ErrBadConn, 0},
		{"failed transaction discards the connection", false, 'E', nil, driver.ErrBadConn, 0},
		{"failed DISCARD ALL discards the connection", false, 'I', errors.New("conn closed"), driver.ErrBadConn, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &sessionEpoch{}
			s := &fakePgSession{custom: map[string]any{}, tx: c.tx, fail: c.fail}
			e.mark(s.custom)
			if !c.fresh {
				e.advance()
			}
			err := resetPostgres(context.Background(), e, s)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if s.discards != c.wantDiscards {
				t.Errorf("DISCARD ALL ran %d times, want %d", s.discards, c.wantDiscards)
			}
		})
	}
}

func TestResetPostgresOncePerCheckout(t *testing.T) {
	e := &sessionEpoch{}
	s := &fakePgSession{custom: map[string]any{}, tx: 'I'}
	e.mark(s.custom)
	for checkout := 1; checkout <= 3; checkout++ {
		e.advance()
		for stmt := 0; stmt < 4; stmt++ {
			if err := resetPostgres(context.Background(), e, s); err != nil {
				t.Fatal(err)
			}
		}
		if s.discards != checkout {
			t.Fatalf("after checkout %d: %d resets, want %d", checkout, s.discards, checkout)
		}
	}
}

func TestResetsSession(t *testing.T) {
	cases := []struct {
		name string
		db   DB
		want bool
	}{
		{"reusable postgres", &SQL{kind: "postgres", epoch: &sessionEpoch{}}, true},
		{"plain postgres", &SQL{kind: "postgres"}, false},
		{"mssql", &SQL{kind: "mssql"}, true},
		{"odbc", &SQL{kind: "odbc"}, true},
		{"clickhouse over http", &SQL{kind: "clickhouse", stateless: true}, true},
		{"clickhouse native", &SQL{kind: "clickhouse"}, false},
		{"mysql", &SQL{kind: "mysql"}, false},
		{"redis", &Redis{}, false},
		{"mongodb", &Mongo{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResetsSession(c.db); got != c.want {
				t.Errorf("ResetsSession = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOpenReusableSetsResetCapability(t *testing.T) {
	cases := []struct {
		name    string
		profile map[string]any
		want    bool
	}{
		{"postgres", map[string]any{"adapter": "postgres", "host": "127.0.0.1", "dbname": "x"}, true},
		{"clickhouse http", map[string]any{"adapter": "clickhouse", "host": "127.0.0.1", "port": int64(8123)}, true},
		{"clickhouse native", map[string]any{"adapter": "clickhouse", "host": "127.0.0.1", "port": int64(9000)}, false},
		{"mysql", map[string]any{"adapter": "mysql", "host": "127.0.0.1"}, false},
		{"redis", map[string]any{"adapter": "redis", "host": "127.0.0.1"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Opening is lazy for every adapter here: nothing is dialed.
			db, err := OpenReusable(context.Background(), c.profile, "")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if got := ResetsSession(db); got != c.want {
				t.Errorf("ResetsSession = %v, want %v", got, c.want)
			}
			// go-redis normalizes -1 (no retries) to 0 and 0 (unset) to 3.
			if r, ok := db.(*Redis); ok && r.client.Options().MaxRetries != 0 {
				t.Errorf("reusable redis MaxRetries = %d, want 0 (no retries)", r.client.Options().MaxRetries)
			}
		})
	}
}
