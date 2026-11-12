package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Snarap1/hqdb/internal/server"
)

// The masking cases use literals instead of tables: each tool call opens its
// own connection, so nothing has to exist in the shared fixtures.

const maskedSQL = "SELECT 'ivan@example.com' AS email, 'ivan@example.com' AS contact_note, 'bolt' AS title"

func TestE2EMaskedRunQueryMasksRowsAndNamesColumns(t *testing.T) {
	s := startE2E(t)
	var out server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": "pg-masked", "sql": maskedSQL}, &out)
	if len(out.Rows) != 1 || len(out.Rows[0]) != 3 {
		t.Fatalf("rows = %+v", out.Rows)
	}
	row := out.Rows[0]
	email, _ := row[0].(string)
	note, _ := row[1].(string)
	if !strings.HasPrefix(email, "masked-") || len(email) != len("masked-")+12 {
		t.Errorf("email = %v, want a masked-<12 hex> marker", row[0])
	}
	if note != email {
		t.Errorf("equal values must share a marker across columns: %v vs %v", row[1], row[0])
	}
	if row[2] != "bolt" {
		t.Errorf("title = %v, want it in clear", row[2])
	}
	if want := []string{"email", "contact_note"}; !reflect.DeepEqual(out.MaskedColumns, want) {
		t.Errorf("masked_columns = %v, want %v", out.MaskedColumns, want)
	}
}

func TestE2EMaskedMarkersLinkAcrossCallsInOneSession(t *testing.T) {
	s := startE2E(t)
	var a, b server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": "pg-masked", "sql": maskedSQL}, &a)
	s.ok("run_query", map[string]any{"profile": "pg-masked", "sql": "SELECT 'IVAN@example.com' AS e"}, &b)
	if a.Rows[0][0] != b.Rows[0][0] {
		t.Errorf("one session must give one marker: %v vs %v", a.Rows[0][0], b.Rows[0][0])
	}
}

func TestE2EUnmaskedProfileIsUnchanged(t *testing.T) {
	s := startE2E(t)
	var out server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": "pg-dev", "sql": maskedSQL}, &out)
	if out.Rows[0][0] != "ivan@example.com" || out.Rows[0][1] != "ivan@example.com" {
		t.Errorf("rows = %+v, want clear values on an unmasked profile", out.Rows)
	}
	if len(out.MaskedColumns) != 0 {
		t.Errorf("masked_columns = %v, want none", out.MaskedColumns)
	}
}

func TestE2EMaskedErrorTextIsMasked(t *testing.T) {
	s := startE2E(t)
	const bad = "SELECT 'ivan@example.com'::int"
	masked := s.errText("run_query", map[string]any{"profile": "pg-masked", "sql": bad})
	if strings.Contains(masked, "ivan@example.com") || !strings.Contains(masked, "masked-") {
		t.Errorf("masked error = %q, want the email replaced by a marker", masked)
	}
	clear := s.errText("run_query", map[string]any{"profile": "pg-dev", "sql": bad})
	if !strings.Contains(clear, "ivan@example.com") {
		t.Errorf("unmasked error = %q, want the value in clear", clear)
	}
}

func TestE2EMaskedProfileRefusesMarkersWhateverTheAccessMode(t *testing.T) {
	s := startE2E(t)
	const marker = "masked-0123456789ab"
	reason := "statement contains a masked value (" + marker + "); masked values are placeholders, not data"
	for _, profile := range []string{"pg-masked", "pg-masked-rw"} {
		for _, tool := range []string{"run_query", "export_query"} {
			args := map[string]any{"profile": profile, "sql": "SELECT '" + marker + "'"}
			if tool == "export_query" {
				args["dest_path"] = filepath.Join(t.TempDir(), "out.csv")
			}
			got := s.errText(tool, args)
			if want := "Refused (masked profile): " + reason; got != want {
				t.Errorf("%s %s: got %q, want %q", profile, tool, got, want)
			}
		}
	}
	// An unmasked profile treats the same text as an ordinary string.
	var out server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": "pg-dev", "sql": "SELECT '" + marker + "'"}, &out)
}

func TestE2EMaskedExportMasksRows(t *testing.T) {
	s := startE2E(t)
	dir := t.TempDir()
	for _, format := range []string{"csv", "json", "ndjson"} {
		dest := filepath.Join(dir, "out."+format)
		var out server.ExportQueryOut
		s.ok("export_query", map[string]any{"profile": "pg-masked", "sql": maskedSQL, "dest_path": dest}, &out)
		if out.RowCount != 1 {
			t.Fatalf("%s: row_count = %d", format, out.RowCount)
		}
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "ivan@example.com") || !strings.Contains(text, "masked-") || !strings.Contains(text, "bolt") {
			t.Errorf("%s export = %q, want masked emails and a clear title", format, text)
		}
	}
}
