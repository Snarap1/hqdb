package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Snarap1/hqdb/internal/server"
)

// bigQueries yields millions of rows per SQL adapter, so reading the whole
// result would take seconds.
var bigQueries = map[string]string{
	// generate_series in the select list streams its rows; in FROM it
	// would build the whole series before sending the first one.
	"pg-dev":        "SELECT generate_series(1, 10000000) AS g",
	"my-dev":        "SELECT a.ORDINAL_POSITION FROM information_schema.COLUMNS a CROSS JOIN information_schema.COLUMNS b",
	"ms-dev":        "SELECT a.object_id FROM sys.all_objects a CROSS JOIN sys.all_objects b",
	"ms-odbc-dev":   "SELECT a.object_id FROM sys.all_objects a CROSS JOIN sys.all_objects b",
	"ch-dev":        "SELECT number FROM numbers(300000000)",
	"ch-native-dev": "SELECT number FROM numbers(300000000)",
}

// TestE2ERunQueryLimitStopsServer proves that a small limit on a huge
// result comes back truncated, without an error, and without reading the
// rest of the result from the server.
func TestE2ERunQueryLimitStopsServer(t *testing.T) {
	s := startE2E(t)
	for profile, sqlText := range bigQueries {
		start := time.Now()
		var out server.RunQueryOut
		s.ok("run_query", map[string]any{"profile": profile, "sql": sqlText, "limit": 3}, &out)
		elapsed := time.Since(start)
		t.Logf("%s: run_query limit 3 took %v", profile, elapsed)
		if out.RowCount != 3 || len(out.Rows) != 3 || !out.Truncated {
			t.Errorf("%s: row_count = %d, rows = %d, truncated = %v; want 3, 3, true",
				profile, out.RowCount, len(out.Rows), out.Truncated)
		}
		if elapsed > 3*time.Second {
			t.Errorf("%s: run_query limit 3 took %v, want it to stop the server early", profile, elapsed)
		}
	}
}

// TestE2EExportQueryLimitStopsServer is the export_query counterpart.
func TestE2EExportQueryLimitStopsServer(t *testing.T) {
	s := startE2E(t)
	dir := t.TempDir()
	for profile, sqlText := range bigQueries {
		dest := filepath.Join(dir, profile+".csv")
		start := time.Now()
		var out server.ExportQueryOut
		s.ok("export_query", map[string]any{
			"profile": profile, "sql": sqlText, "dest_path": dest, "limit": 3,
		}, &out)
		elapsed := time.Since(start)
		t.Logf("%s: export_query limit 3 took %v", profile, elapsed)
		if out.RowCount != 3 {
			t.Errorf("%s: row_count = %d, want 3", profile, out.RowCount)
		}
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		if lines := strings.Count(string(data), "\n"); lines != 4 {
			t.Errorf("%s: export has %d lines, want a header and 3 rows: %q", profile, lines, data)
		}
		if elapsed > 3*time.Second {
			t.Errorf("%s: export_query limit 3 took %v, want it to stop the server early", profile, elapsed)
		}
	}
}

// TestE2ERunQueryLimitLeavesNoServerQuery checks the server side: a
// truncated run_query returns at once, and its statement does not keep
// running on the server afterwards. Each statement would take minutes to
// read in full. The running check matches the statement text exactly, so
// it never matches itself.
func TestE2ERunQueryLimitLeavesNoServerQuery(t *testing.T) {
	s := startE2E(t)
	cases := []struct{ profile, big, running string }{
		{"pg-dev", "SELECT generate_series(1, 200000000) AS g",
			"SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND query = '%s'"},
		{"my-dev", "SELECT a.ORDINAL_POSITION, b.ORDINAL_POSITION FROM information_schema.COLUMNS a, information_schema.COLUMNS b",
			"SELECT count(*) FROM information_schema.PROCESSLIST WHERE INFO = '%s'"},
		{"ch-dev", "SELECT number FROM numbers(100000000000)",
			"SELECT count() FROM system.processes WHERE query = '%s'"},
		{"ch-native-dev", "SELECT number AS n FROM numbers(100000000000)",
			"SELECT count() FROM system.processes WHERE query = '%s'"},
	}
	for _, c := range cases {
		start := time.Now()
		var out server.RunQueryOut
		s.ok("run_query", map[string]any{"profile": c.profile, "sql": c.big, "limit": 3}, &out)
		if !out.Truncated || out.RowCount != 3 {
			t.Errorf("%s: row_count = %d, truncated = %v; want 3 and true", c.profile, out.RowCount, out.Truncated)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("%s: run_query limit 3 took %v, want it to stop the server early", c.profile, elapsed)
		}
		time.Sleep(time.Second)
		s.ok("run_query", map[string]any{"profile": c.profile, "sql": fmt.Sprintf(c.running, c.big)}, &out)
		if n, _ := out.Rows[0][0].(float64); n != 0 {
			t.Errorf("%s: the truncated query is still running on the server: %v", c.profile, out.Rows[0][0])
		}
	}
}
