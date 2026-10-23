package ui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A postgres Profile without dbname shows its discovered Databases as the
// top Catalog level (ADR-0004), and a Table opens from each of them on that
// Database's own connection.
func TestPostgresMultiDatabaseProfileOpensTablesFromEachDatabase(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-multi")

	selectPath(t, tm, "pg-multi", "hqdb", "public", "widgets")
	enter(tm)
	waitForText(t, tm, "DATA pg-multi hqdb.public.widgets")

	// The tab area has focus now; the second Database sits beside the first.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	selectPath(t, tm, "hqdb_two", "public", "gadgets")
	enter(tm)
	screen := waitForText(t, tm, "DATA pg-multi hqdb_two.public.gadgets")
	if !strings.Contains(screen, "sprocket") {
		t.Errorf("gadgets rows missing:\n%s", screen)
	}
}

// A SQL Console opened on a Database node is bound to that Database and
// names it in its tab title.
func TestPostgresMultiDatabaseConsoleIsBoundToItsDatabase(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-multi")

	selectPath(t, tm, "pg-multi", "hqdb_two")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitForText(t, tm, "hqdb_two console")
	typeText(tm, "SELECT 'db=' || current_database() || '|' AS d;")
	altEnter(tm)
	waitForText(t, tm, "db=hqdb_two|")
}
