package adapters

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
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
		{"narrowed profile", "other", []string{"cfg"}, `database "other" is not available for this profile; available: cfg`},
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

func TestDiscoversDatabases(t *testing.T) {
	tests := []struct {
		name string
		p    map[string]any
		want bool
	}{
		{"postgres without dbname", map[string]any{"adapter": "postgres"}, true},
		{"postgres with dbname", map[string]any{"adapter": "postgres", "dbname": "x"}, false},
		{"postgres url with db", map[string]any{"adapter": "postgres", "conn_str": "postgres://u:p@h/x"}, false},
		{"postgres url without db", map[string]any{"adapter": "postgres", "conn_str": "postgres://u:p@h"}, true},
		{"mysql", map[string]any{"adapter": "mysql"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DiscoversDatabases(tc.p); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
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
	p["dbname"] = "hqdb"
	got, err = Databases(ctx, p)
	if err != nil || !reflect.DeepEqual(got, []string{"hqdb"}) {
		t.Errorf("narrowed: got %v, %v", got, err)
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
