package adapters

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresDSNDatabaseOverride(t *testing.T) {
	base := map[string]any{"adapter": "postgres", "host": "h", "user": "u", "password": "p"}
	withDB := map[string]any{"adapter": "postgres", "host": "h", "user": "u", "password": "p", "dbname": "cfg"}
	tests := []struct {
		name     string
		profile  map[string]any
		override string
		want     string
	}{
		{"no dbname, no override", base, "", "host=h port=5432 user=u password=p sslmode=prefer"},
		{"override fills missing dbname", base, "sales", "host=h port=5432 user=u password=p dbname=sales sslmode=prefer"},
		{"override wins over dbname", withDB, "sales", "host=h port=5432 user=u password=p dbname=sales sslmode=prefer"},
		{"no override keeps dbname", withDB, "", "host=h port=5432 user=u password=p dbname=cfg sslmode=prefer"},
		{"override with space is quoted", base, "my db", "host=h port=5432 user=u password=p dbname='my db' sslmode=prefer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := postgresDSN(tc.profile, tc.override)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckDatabase(t *testing.T) {
	avail := []string{"alpha", "beta"}
	tests := []struct {
		name      string
		requested string
		avail     []string
		want      string // empty means no error
	}{
		{"listed", "beta", avail, ""},
		{"unlisted", "gamma", avail, `database "gamma" is not available for this profile; available: alpha, beta`},
		{"case sensitive", "Alpha", avail, `database "Alpha" is not available for this profile; available: alpha, beta`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDatabase(tc.requested, tc.avail)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDatabasesRefusesAdaptersWithoutDiscovery(t *testing.T) {
	tests := []struct {
		name string
		p    map[string]any
		want string
	}{
		{"mysql", map[string]any{"adapter": "mysql"}, "database discovery is not available for mysql"},
		{"clickhouse", map[string]any{"adapter": "clickhouse"}, "database discovery is not available for clickhouse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Databases(context.Background(), tc.p)
			if err == nil || err.Error() != tc.want {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
	_, err := (&SQL{kind: "mysql"}).Databases(context.Background())
	if err == nil || err.Error() != "database discovery is not available for mysql" {
		t.Errorf("non-postgres connection: got %v", err)
	}
}

func TestDiscoverWithRetry(t *testing.T) {
	pgErr := func(code string) error { return &pgconn.PgError{Severity: "FATAL", Code: code, Message: "m"} }
	tests := []struct {
		name      string
		first     error
		second    error
		want      []string
		wantErr   string // empty means no error
		wantCalls []string
	}{
		{"default works", nil, nil, []string{"a"}, "", []string{""}},
		{"default database missing, postgres works", pgErr("3D000"), nil, []string{"b"}, "", []string{"", "postgres"}},
		{"no CONNECT on default, postgres works", pgErr("42501"), nil, []string{"b"}, "", []string{"", "postgres"}},
		{"pg_hba rejects default, postgres works", pgErr("28000"), nil, []string{"b"}, "", []string{"", "postgres"}},
		{"bad credentials are not retried", pgErr("28P01"), nil, nil, "FATAL: m (SQLSTATE 28P01)", []string{""}},
		{"network failure is not retried", errors.New("dial tcp: refused"), nil, nil, "dial tcp: refused", []string{""}},
		{"cancellation is not retried", context.Canceled, nil, nil, "context canceled", []string{""}},
		{"both fail", pgErr("3D000"), errors.New("boom"), nil,
			"FATAL: m (SQLSTATE 3D000) (retry against database postgres also failed: boom)", []string{"", "postgres"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			got, err := discoverWithRetry(context.Background(), func(_ context.Context, database string) ([]string, error) {
				calls = append(calls, database)
				if database == "" {
					if tc.first != nil {
						return nil, tc.first
					}
					return []string{"a"}, nil
				}
				if tc.second != nil {
					return nil, tc.second
				}
				return []string{"b"}, nil
			})
			if !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Errorf("attempted databases %q, want %q", calls, tc.wantCalls)
			}
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("got %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestBindsDatabaseAndPathDatabase(t *testing.T) {
	for adapter, want := range map[string]bool{"postgres": true, "mysql": false, "mssql": false, "mongodb": false} {
		if got := BindsDatabase(adapter); got != want {
			t.Errorf("BindsDatabase(%q) = %v, want %v", adapter, got, want)
		}
	}
	tests := []struct {
		path []string
		want string
	}{
		{nil, ""},
		{[]string{"db"}, "db"},
		{[]string{"db", "public"}, "db"},
		{[]string{"db", "public", "t"}, "db"},
	}
	for _, tc := range tests {
		if got := PathDatabase(tc.path); got != tc.want {
			t.Errorf("PathDatabase(%v) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func e2ePostgres(t *testing.T) map[string]any {
	t.Helper()
	if os.Getenv("HQDB_E2E") != "1" {
		t.Skip("set HQDB_E2E=1 and run bash testdata/fixtures.sh up first")
	}
	return map[string]any{"adapter": "postgres", "host": "127.0.0.1", "port": 55432, "user": "hq", "password": "hq"}
}

func TestDatabasesE2E(t *testing.T) {
	p := e2ePostgres(t)
	ctx := context.Background()
	got, err := Databases(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hqdb", "hqdb_two"} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("discovered %v, missing %q", got, want)
		}
	}
	for _, g := range got {
		if g == "template0" || g == "template1" {
			t.Errorf("template database %q listed", g)
		}
	}
	// dbname is only where discovery starts; it still lists every database.
	p["dbname"] = "hqdb_two"
	fromDB, err := Databases(ctx, p)
	if err != nil || !reflect.DeepEqual(fromDB, got) {
		t.Errorf("from dbname: got %v, %v, want %v", fromDB, err, got)
	}
}

func TestOpenDatabaseE2E(t *testing.T) {
	p := e2ePostgres(t)
	ctx := context.Background()
	conn, err := OpenDatabase(ctx, p, "hqdb_two")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var name string
	if err := conn.(*SQL).SQL().QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "hqdb_two" {
		t.Errorf("current_database = %q, %v", name, err)
	}
	_, err = OpenDatabase(ctx, p, "nope")
	if err == nil || !strings.Contains(err.Error(), `database "nope" is not available for this profile; available: `) {
		t.Errorf("got %v", err)
	}
	conn2, err := OpenDatabase(ctx, p, "")
	if err != nil {
		t.Fatal(err)
	}
	conn2.Close()
}

func TestOnLoginDefault(t *testing.T) {
	bare := map[string]any{"adapter": "postgres", "host": "h"}
	withDB := map[string]any{"adapter": "postgres", "host": "h", "dbname": "cfg"}
	urlWithDB := map[string]any{"adapter": "postgres", "conn_str": "postgres://u:p@h/cfg"}
	tests := []struct {
		name string
		conn *SQL
		want bool
	}{
		{"no dbname, no database", &SQL{kind: "postgres", profile: bare}, true},
		{"dbname", &SQL{kind: "postgres", profile: withDB}, false},
		{"dbname in a URL conn_str", &SQL{kind: "postgres", profile: urlWithDB}, false},
		{"opened for a database", &SQL{kind: "postgres", profile: bare, database: "sales"}, false},
		{"no profile", &SQL{kind: "postgres"}, false},
		{"not postgres", &SQL{kind: "mssql", profile: map[string]any{"adapter": "mssql"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.conn.OnLoginDefault(); got != tc.want {
				t.Errorf("OnLoginDefault() = %v, want %v", got, tc.want)
			}
		})
	}
}
