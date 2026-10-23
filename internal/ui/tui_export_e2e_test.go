package ui_test

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
)

func TestDataViewExportWritesFilteredRowsBeyondPageToCSVAndJSON(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	pgProfile, err := cfg.Profile("pg-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(pgProfile)
	if err != nil {
		t.Fatalf("connecting to pg-dev to seed an export fixture: %v", err)
	}
	sqlConn := conn.(*adapters.SQL)
	// A unique "zzex<n>" name sorts after the tables other tests rely on
	// being first, and is reached through the name filter, so this test
	// neither perturbs nor depends on the Tables level's ordering.
	target := fmt.Sprintf("zzex%06d", time.Now().UnixNano()%1000000)
	table := "public." + target
	if _, err := sqlConn.SQL().Exec("CREATE TABLE " + table + " (id integer primary key, val text)"); err != nil {
		conn.Close()
		t.Fatalf("seeding export fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlConn.SQL().Exec("DROP TABLE IF EXISTS " + table)
		conn.Close()
	})
	for i := 1; i <= 40; i++ {
		if _, err := sqlConn.SQL().Exec("INSERT INTO "+table+" (id, val) VALUES ($1, $2)", i, fmt.Sprintf("row%02d", i)); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")
	selectNode(t, tm, "pg-dev")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "public")
	selectNode(t, tm, "public")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "  table")
	// Filtering on the full unique name leaves the target as the only
	// visible row, so it is selected whatever else "public" holds.
	tm.Send(tea.KeyPressMsg{Code: '/', Text: "/"})
	typeText(tm, target)
	waitForFrameContaining(t, tm.Output(), target+"  table")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "1-30")

	// 38 rows match: more than the 30-row page.
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	typeText(tm, "id > 2")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: '#', Text: "#"})
	waitForText(t, tm, "of 38")
	// Keys are handled in order, so the export below sees this ORDER BY.
	tm.Send(tea.KeyPressMsg{Code: 'o', Text: "o"})
	typeText(tm, "id")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	dir := t.TempDir()
	csvPath := filepath.Join(dir, "out.csv")
	tm.Send(tea.KeyPressMsg{Code: 'e', Text: "e"})
	typeText(tm, csvPath)
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForFrameContaining(t, tm.Output(), "csv): 38 rows written")

	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 39 {
		t.Fatalf("csv has %d records, want header + 38", len(records))
	}
	if got := fmt.Sprint(records[0], records[1], records[38]); got != "[id val] [3 row03] [40 row40]" {
		t.Errorf("csv header/first/last = %s", got)
	}

	jsonPath := filepath.Join(dir, "out.json")
	tm.Send(tea.KeyPressMsg{Code: 'e', Text: "e"})
	typeText(tm, jsonPath)
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForText(t, tm, "export (json): 38 rows written")

	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("json export does not parse: %v", err)
	}
	if len(rows) != 38 || rows[0]["val"] != "row03" || rows[37]["val"] != "row40" {
		t.Errorf("json export rows = %d, first %v, last %v", len(rows), rows[0], rows[len(rows)-1])
	}

	// An extension export cannot infer shows export.InferFormat's error.
	tm.Send(tea.KeyPressMsg{Code: 'e', Text: "e"})
	typeText(tm, filepath.Join(dir, "out.txt"))
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForText(t, tm, "cannot infer format")
}
