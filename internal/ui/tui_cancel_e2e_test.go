package ui_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
)

func ctrlC(tm interface{ Send(tea.Msg) }) {
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
}

func TestCtrlCCancelsConsoleStatementAndConsoleStaysUsable(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")
	selectNode(t, tm, "pg-dev")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitForFrameContaining(t, tm.Output(), "pg-dev  SQL console")

	// No default timeout: a statement that takes a while still finishes.
	typeText(tm, "SELECT pg_sleep(2), 'slept' || 'ok' AS s;")
	altEnter(tm)
	waitForFrameContaining(t, tm.Output(), "Running...")
	start := time.Now()
	waitForFrameContaining(t, tm.Output(), "sleptok")
	if time.Since(start) < time.Second {
		t.Errorf("pg_sleep(2) finished in %s; it did not really run", time.Since(start))
	}

	runInConsole(tm, "SELECT pg_sleep(60)")
	waitForFrameContaining(t, tm.Output(), "Running...")
	start = time.Now()
	ctrlC(tm)
	// waitForFrame gives up after 5s, far below the 60s statement.
	waitForFrameContaining(t, tm.Output(), "statement cancelled; session reset")
	t.Logf("cancelled after %s", time.Since(start))

	// The app is still running and the Console still runs statements.
	runInConsole(tm, "SELECT 'after' || 'cancel' AS s")
	waitForFrameContaining(t, tm.Output(), "aftercancel")

	// Ctrl+C with nothing running neither quits nor changes the result.
	ctrlC(tm)
	runInConsole(tm, "SELECT 'still' || 'alive' AS s")
	waitForFrameContaining(t, tm.Output(), "stillalive")
}

func TestCtrlCCancelsDataViewPageCountAndExport(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	pgProfile, err := cfg.Profile("pg-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(pgProfile)
	if err != nil {
		t.Fatalf("connecting to pg-dev to seed a cancel fixture: %v", err)
	}
	sqlConn := conn.(*adapters.SQL)
	// A unique "zzcan<n>" name sorts after the tables other tests rely on
	// and is reached through the name filter.
	target := fmt.Sprintf("zzcan%06d", time.Now().UnixNano()%1000000)
	table := "public." + target
	if _, err := sqlConn.SQL().Exec("CREATE TABLE " + table + " (id integer primary key)"); err != nil {
		conn.Close()
		t.Fatalf("seeding cancel fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlConn.SQL().Exec("DROP TABLE IF EXISTS " + table)
		conn.Close()
	})
	if _, err := sqlConn.SQL().Exec("INSERT INTO " + table + " VALUES (1)"); err != nil {
		t.Fatal(err)
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
	tm.Send(tea.KeyPressMsg{Code: '/', Text: "/"})
	typeText(tm, target)
	waitForFrameContaining(t, tm.Output(), target+"  table")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "1-1")

	// Every query under this WHERE sleeps for a minute per row.
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	typeText(tm, "pg_sleep(60) IS NOT NULL")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForText(t, tm, "loading...")
	ctrlC(tm)
	waitForText(t, tm, "page load cancelled")

	tm.Send(tea.KeyPressMsg{Code: '#', Text: "#"})
	waitForFrameContaining(t, tm.Output(), "(counting...)")
	ctrlC(tm)
	waitForFrameContaining(t, tm.Output(), "count cancelled")

	tm.Send(tea.KeyPressMsg{Code: 'e', Text: "e"})
	typeText(tm, filepath.Join(t.TempDir(), "out.csv"))
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForFrameContaining(t, tm.Output(), "exporting...")
	ctrlC(tm)
	// Only the changed cells redraw, and "exporting..." shares a prefix
	// with "export cancelled".
	waitForFrameContaining(t, tm.Output(), "cancelled")

	// The tab stays usable: a plain WHERE loads and counts again.
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	for range len("pg_sleep(60) IS NOT NULL") {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(tm, "id = 1")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	// Bubble Tea redraws only changed cells, and both the reloaded "1-1"
	// indicator and the WHERE line share prefixes with what is already on
	// screen, so neither shows up contiguously in every run. The count
	// under the new WHERE ("of 1", never drawn before since every earlier
	// count was cancelled) proves the tab works again; keys are processed
	// in order, so "#" counts under "id = 1".
	tm.Send(tea.KeyPressMsg{Code: '#', Text: "#"})
	waitForFrameContaining(t, tm.Output(), "of 1")
}
