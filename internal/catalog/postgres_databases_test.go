package catalog

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/Snarap1/hqdb/internal/adapters"
)

func names(nodes []SchemaNode) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}

func TestPostgresMultiDatabaseSchemaE2E(t *testing.T) {
	if os.Getenv("HQDB_E2E") != "1" {
		t.Skip("set HQDB_E2E=1 and run bash testdata/fixtures.sh up first")
	}
	ctx := context.Background()
	p := map[string]any{"adapter": "postgres", "host": "127.0.0.1", "port": 55432, "user": "hq", "password": "hq"}
	conn, err := adapters.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	out, err := GetSchema(ctx, conn, nil, "", false, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	got := names(out.Nodes)
	for _, want := range []string{"hqdb", "hqdb_two"} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("databases %v missing %q", got, want)
		}
	}
	if out.Nodes[0].Kind != "database" {
		t.Errorf("kind = %q", out.Nodes[0].Kind)
	}

	// Deeper levels run on the named database, not the connection's default.
	out, err = GetSchema(ctx, conn, []string{"hqdb_two", "public"}, "", false, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(out.Nodes); !reflect.DeepEqual(got, []string{"gadgets"}) {
		t.Errorf("hqdb_two.public tables = %v", got)
	}
	out, err = GetSchema(ctx, conn, []string{"hqdb_two"}, "", false, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(out.Nodes); !reflect.DeepEqual(got, []string{"public"}) {
		t.Errorf("hqdb_two schemas = %v", got)
	}

	if _, err = GetSchema(ctx, conn, []string{"nope"}, "", false, DefaultLimits); err == nil {
		t.Error("unknown database was not refused")
	}

	// A Profile with dbname is narrowed to it.
	p["dbname"] = "hqdb"
	narrow, err := adapters.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer narrow.Close()
	out, err = GetSchema(ctx, narrow, nil, "", false, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(out.Nodes); !reflect.DeepEqual(got, []string{"hqdb"}) {
		t.Errorf("narrowed databases = %v", got)
	}
	if _, err := GetSchema(ctx, narrow, []string{"hqdb_two"}, "", false, DefaultLimits); err == nil {
		t.Error("narrowed profile opened another database")
	}
}
