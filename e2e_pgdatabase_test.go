package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Snarap1/hqdb/internal/catalog"
	"github.com/Snarap1/hqdb/internal/ddl"
	"github.com/Snarap1/hqdb/internal/server"
)

// TestE2EPostgresDatabaseArgument queries two databases through the one
// discovering pg-multi Profile, and checks that the argument is refused when
// unknown and ignored by a non-postgres adapter.
func TestE2EPostgresDatabaseArgument(t *testing.T) {
	s := startE2E(t)
	var gadgets server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": "pg-multi", "database": "hqdb_two", "sql": "SELECT name FROM gadgets"}, &gadgets)
	if gadgets.RowCount != 1 || gadgets.Rows[0][0] != "sprocket" {
		t.Errorf("hqdb_two rows = %+v, want sprocket", gadgets.Rows)
	}
	var widgets server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": "pg-multi", "database": "hqdb", "sql": "SELECT count(*) FROM widgets"}, &widgets)
	if widgets.RowCount != 1 {
		t.Errorf("hqdb rows = %+v, want one count row", widgets.Rows)
	}
	msg := s.errText("run_query", map[string]any{"profile": "pg-multi", "database": "nope", "sql": "SELECT 1"})
	if !strings.Contains(msg, `database "nope" is not available`) || !strings.Contains(msg, "hqdb_two") {
		t.Errorf("unknown database message = %q, want it to name the available ones", msg)
	}
	msg = s.errText("run_query", map[string]any{"profile": "pg-multi", "database": "hqdb_two", "sql": "DELETE FROM gadgets"})
	if !strings.HasPrefix(msg, "Refused (read-only profile): ") {
		t.Errorf("write on a chosen database: message = %q, want the read-only refusal", msg)
	}
	var mysql server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": "my-dev", "database": "no_such_db", "sql": "SELECT 1"}, &mysql)
	if mysql.RowCount != 1 {
		t.Errorf("mysql with ignored database: rows = %+v", mysql.Rows)
	}
}

func TestE2EPostgresDatabaseExport(t *testing.T) {
	s := startE2E(t)
	dest := filepath.Join(t.TempDir(), "gadgets.csv")
	s.ok("export_query", map[string]any{"profile": "pg-multi", "database": "hqdb_two", "sql": "SELECT name FROM gadgets", "dest_path": dest}, nil)
	raw, err := os.ReadFile(dest)
	if err != nil || !strings.Contains(string(raw), "sprocket") {
		t.Errorf("export = %q, %v; want sprocket", raw, err)
	}
}

func TestE2EPostgresSchemaColumnsDDLByPathDatabase(t *testing.T) {
	s := startE2E(t)
	var root catalog.SchemaOut
	s.ok("get_schema", map[string]any{"profile": "pg-multi", "path": []string{}}, &root)
	if names := nodeNames(root.Nodes); !names["hqdb"] || !names["hqdb_two"] {
		t.Errorf("root nodes = %+v, want hqdb and hqdb_two", root.Nodes)
	}
	var cols catalog.ColumnsOut
	s.ok("get_columns", map[string]any{"profile": "pg-multi", "path": []string{"hqdb_two", "public", "gadgets"}}, &cols)
	if cols.Table != "public.gadgets" || len(cols.Columns) != 2 {
		t.Errorf("columns = %+v, want public.gadgets with 2 columns", cols)
	}
	var d ddl.Out
	s.ok("get_ddl", map[string]any{"profile": "pg-multi", "path": []string{"hqdb_two", "public", "gadgets"}}, &d)
	if !strings.Contains(d.Definition, "gadgets") {
		t.Errorf("ddl = %q, want gadgets", d.Definition)
	}
	msg := s.errText("get_columns", map[string]any{"profile": "pg-multi", "path": []string{"nope", "public", "gadgets"}})
	if !strings.Contains(msg, `database "nope" is not available`) {
		t.Errorf("unknown database message = %q", msg)
	}
}
