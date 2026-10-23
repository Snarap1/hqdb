package ui_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/ui"
)

// loadFixtureConfig builds a temporary hqdb config from the dev fixture
// config (the same one the MCP e2e suite uses), with every fixture Profile
// made read-write so the Consoles can write after confirmation, plus any
// extra TOML the caller appends, and loads it the real way through
// config.Load. Gated behind HQDB_E2E=1 like the existing e2e suite, since
// it exercises the same docker fixtures.
func loadFixtureConfig(t *testing.T, extraTOML string) *config.Config {
	t.Helper()
	return loadFixtureConfigAs(t, config.ReadWrite, extraTOML)
}

// loadFixtureConfigAs is loadFixtureConfig with every fixture Profile set
// to access.
func loadFixtureConfigAs(t *testing.T, access config.Access, extraTOML string) *config.Config {
	t.Helper()
	if os.Getenv("HQDB_E2E") != "1" {
		t.Skip("set HQDB_E2E=1 and run bash testdata/fixtures.sh up first")
	}
	base, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures.toml"))
	if err != nil {
		t.Fatalf("read fixtures.toml: %v", err)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "hqdb.toml")
	var b strings.Builder
	for _, line := range strings.SplitAfter(string(base), "\n") {
		b.WriteString(line)
		if strings.HasPrefix(line, "[profiles.") {
			fmt.Fprintf(&b, "access = %q\n", access)
		}
	}
	content := b.String() + "\n" + extraTOML
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HQDB_CONFIG", cfgPath)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// startTUI starts the root model against cfg in an 100x30 virtual terminal.
func startTUI(t *testing.T, cfg *config.Config) *teatest.TestModel {
	t.Helper()
	tm := teatest.NewTestModel(t, ui.New(cfg), teatest.WithInitialTermSize(120, 30))
	t.Cleanup(func() {
		tm.Quit()
	})
	return tm
}

// waitForFrame drains r until match reports true for the bytes produced
// since the last drain, and returns exactly those bytes. Bubble Tea only
// redraws the lines that changed, so a value that is still on screen but
// unchanged since the last drain will not reappear in that delta; tests
// must match on something the action in question actually redraws (a
// changed line), not on static content elsewhere on the screen.
func waitForFrame(t *testing.T, r io.Reader, match func(frame []byte) bool) []byte {
	t.Helper()
	var frame []byte
	teatest.WaitFor(t, r, func(bts []byte) bool {
		if match(bts) {
			frame = append([]byte(nil), bts...)
			return true
		}
		return false
	}, teatest.WithDuration(5*time.Second))
	if frame == nil {
		t.Fatalf("no matching frame arrived")
	}
	return frame
}

// waitForFrameContaining is waitForFrame for the common case of matching a
// single substring.
func waitForFrameContaining(t *testing.T, r io.Reader, want string) []byte {
	t.Helper()
	return waitForFrame(t, r, func(bts []byte) bool {
		return bytes.Contains(bts, []byte(want))
	})
}

func TestTreeListsEveryProfileWithAdapterAndNoConnection(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)

	waitForFrameContaining(t, tm.Output(), "pg-dev")
	screen := repaint(t, tm)
	for _, want := range []string{
		"pg-dev  postgres",
		"my-dev  mysql",
		"ms-dev  mssql",
		"ms-odbc-dev  odbc",
		"ch-dev  clickhouse",
		"redis-dev  redis",
		"mongo-dev  mongodb",
		"No tabs open.",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen missing %q:\n%s", want, screen)
		}
	}
}

func TestBrokenProfileShowsFullErrorOnSelection(t *testing.T) {
	cfg := loadFixtureConfig(t, `
[profiles.a-broken-dev]
adapter = "postgres"
not_a_real_key = "x"
`)
	tm := startTUI(t, cfg)

	// a-broken-dev sorts first, so it is selected on start and its error
	// shows in the tab area without pressing anything. "▌✕" is the
	// selection bar plus the broken marker: "▌" because it is selected,
	// "✕" because it failed validation.
	waitForFrameContaining(t, tm.Output(), "a-broken-dev")
	screen := waitForScreen(t, tm, func(s string) bool { return strings.Contains(s, "failed validation") })
	for _, want := range []string{
		"▌✕ a-broken-dev  invalid",
		"failed validation",
		`unknown option "not_a_real_key"`,
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen missing %q\ngot:\n%s", want, screen)
		}
	}
}

func TestGlobalKeysToggleFocusTreeAndHelp(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)

	// Tree is focused on start: the status bar shows the tree's own hints.
	frame := waitForFrameContaining(t, tm.Output(), "pg-dev")
	if !bytes.Contains(frame, []byte("select up")) {
		t.Errorf("tree-focused status bar missing its own hints:\n%s", frame)
	}

	// Tab moves focus to the (empty) tab area. "No tabs open." does not
	// change and so is not redrawn; the status bar losing the tree's own
	// hints is what actually changes on screen, so that is what we match.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "TABS") && !strings.Contains(s, "select up")
	})

	// Shift+Tab moves focus back to the tree; its hints reappear.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	waitForFrameContaining(t, tm.Output(), "select up")

	// z hides the tree entirely: a real layout change, so the tab area's
	// placeholder is redrawn (now full width) and the tree's rows are not.
	tm.Send(tea.KeyPressMsg{Code: 'z', Text: "z"})
	frame = waitForFrame(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("No tabs open."))
	})
	if bytes.Contains(frame, []byte("pg-dev")) {
		t.Errorf("tree should be hidden after z:\n%s", frame)
	}

	// z again shows it.
	tm.Send(tea.KeyPressMsg{Code: 'z', Text: "z"})
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	// ? opens the keymap help over the screen.
	tm.Send(tea.KeyPressMsg{Code: '?', Text: "?"})
	screen := waitForScreen(t, tm, func(s string) bool { return strings.Contains(s, "double-click to open") })
	if !strings.Contains(screen, "hide/show tree") {
		t.Errorf("help overlay missing a key description:\n%s", screen)
	}

	// Esc leaves the help overlay, returning to the normal layout.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "No tabs open.") && !strings.Contains(s, "double-click to open")
	})

	// q quits the program.
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func enter(tm *teatest.TestModel) {
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestExpandPostgresProfileWalksDatabaseSchemaTableColumn(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	selectNode(t, tm, "pg-dev")
	enter(tm) // pg-dev -> Database
	waitForFrameContaining(t, tm.Output(), "hqdb")

	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Schemas (pg_catalog/information_schema hidden)
	frame := waitForFrameContaining(t, tm.Output(), "public")
	for _, want := range []string{"analytics", "public"} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Errorf("frame missing schema %q:\n%s", want, frame)
		}
	}
	for _, unwanted := range []string{"pg_catalog", "information_schema"} {
		if bytes.Contains(frame, []byte(unwanted)) {
			t.Errorf("system schema %q must be hidden:\n%s", unwanted, frame)
		}
	}

	selectNode(t, tm, "public")
	enter(tm) // public -> Tables
	waitForFrameContaining(t, tm.Output(), "widgets  table")

	selectNode(t, tm, "widgets")
	enter(tm) // widgets -> Columns
	// The tree pane is a fixed 34 columns wide, and a Column row at this
	// adapter's depth (Profile/Database/Schema/Table/Column) is indented
	// 8 spaces; "name  text NULL" is the one row of the three that is
	// short enough to render in full, so it both proves the column names
	// made it onto the tree and that nullability renders distinctly. "id"
	// and "price" are still asserted by name only, since their full
	// "...  NOT NULL" / "...  NULL" text is clipped at this width - that
	// clipping is the tree's existing fixed-width rendering, not something
	// this ticket changes.
	frame = waitForFrameContaining(t, tm.Output(), "name  text NULL")
	for _, want := range []string{"id", "name", "price"} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Errorf("frame missing column %q:\n%s", want, frame)
		}
	}
}

func TestExpandMysqlProfileWalksDatabaseTableColumnWithNoSchemaLevel(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "my-dev")

	selectNode(t, tm, "my-dev")
	enter(tm) // my-dev -> Databases (mysql/information_schema/performance_schema/sys hidden)
	frame := waitForFrameContaining(t, tm.Output(), "hqdb2")
	for _, unwanted := range []string{"▸ mysql", "performance_schema", "information_schema"} {
		if bytes.Contains(frame, []byte(unwanted)) {
			t.Errorf("system database %q must be hidden:\n%s", unwanted, frame)
		}
	}

	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Tables directly, mysql has no Schema level
	waitForFrameContaining(t, tm.Output(), "widgets  table")

	selectNode(t, tm, "widgets")
	enter(tm) // widgets -> Columns
	frame = waitForFrameContaining(t, tm.Output(), "NOT NULL")
	for _, want := range []string{"id", "name", "price"} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Errorf("frame missing column %q:\n%s", want, frame)
		}
	}
}

func TestExpandClickhouseProfileShowsDatabases(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "ch-dev")

	enter(tm) // ch-dev sorts first: no navigation needed
	waitForFrameContaining(t, tm.Output(), "hqdb")
}

func TestExpandMssqlProfileShowsSchemas(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "ms-dev")

	selectNode(t, tm, "ms-dev")
	enter(tm) // ms-dev -> Database (one node, like postgres)
	waitForFrameContaining(t, tm.Output(), "hqdb")

	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Schemas
	waitForFrameContaining(t, tm.Output(), "analytics")
}

func TestTableKindMarksViewDistinctlyFromTable(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	pgProfile, err := cfg.Profile("pg-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(pgProfile)
	if err != nil {
		t.Fatalf("connecting to pg-dev to seed a view: %v", err)
	}
	sqlConn := conn.(*adapters.SQL)
	// "wview" (not a longer, more descriptive name): a Table-level row at
	// this adapter's depth is indented 6 spaces inside the fixed 34-column
	// tree pane, and "  view" alone is 7 of the columns left after that.
	if _, err := sqlConn.SQL().Exec("CREATE OR REPLACE VIEW public.wview AS SELECT id FROM public.widgets"); err != nil {
		conn.Close()
		t.Fatalf("seeding a view: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlConn.SQL().Exec("DROP VIEW IF EXISTS public.wview")
		conn.Close()
	})

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
	frame := waitForFrameContaining(t, tm.Output(), "wview")
	for _, want := range []string{"widgets  table", "wview  view"} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Errorf("frame missing %q:\n%s", want, frame)
		}
	}
}

func TestExpandMongoProfileWalksDatabaseCollectionSampledFieldsAndOpensDataView(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "mongo-dev")

	selectNode(t, tm, "mongo-dev")
	enter(tm) // mongo-dev -> Databases (admin/config/local hidden)
	frame := waitForFrameContaining(t, tm.Output(), "hqdb")
	for _, unwanted := range []string{"admin", "config", "local"} {
		if bytes.Contains(frame, []byte(unwanted)) {
			t.Errorf("system database %q must be hidden:\n%s", unwanted, frame)
		}
	}

	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Collections
	frame = waitForFrameContaining(t, tm.Output(), "widgets  collection")
	if !bytes.Contains(frame, []byte("cheap_widgets  view")) {
		t.Errorf("frame missing cheap_widgets view:\n%s", frame)
	}

	selectNode(t, tm, "widgets")
	enter(tm) // widgets -> sampled fields in the tree, and a Data view tab with its Documents
	frame = waitForFrameContaining(t, tm.Output(), "DATA mongo-dev hqdb.widgets")
	for _, want := range []string{
		"tags", "note", // field names sampled only off some of the 3 documents
		`"bolt"`, `"nut"`, `"screw"`, // the Data view's first page, as cards
	} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Errorf("frame missing %q:\n%s", want, frame)
		}
	}
}

func TestDeadProfileConnectFailureMarksBrokenAndRetryReattempts(t *testing.T) {
	cfg := loadFixtureConfig(t, `
[profiles.dead-dev]
adapter = "postgres"
host = "127.0.0.1"
port = 1
`)
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "dead-dev")

	selectNode(t, tm, "dead-dev")
	enter(tm)
	frame := waitForFrameContaining(t, tm.Output(), "failed to connect")
	if !bytes.Contains(frame, []byte("✕")) {
		t.Errorf("broken marker missing:\n%s", frame)
	}

	// r retries the connection. The server is still unreachable, so the
	// retry reproduces the identical broken state: nothing on screen
	// changes, and Bubble Tea only redraws lines that did change, so
	// waiting for "failed to connect" to reappear would hang (that text
	// never left the screen in the first place). Instead, confirm the
	// program is still alive and responsive by moving the selection and
	// observing a fresh, genuinely different frame.
	tm.Send(tea.KeyPressMsg{Code: 'r', Text: "r"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	waitForFrameContaining(t, tm.Output(), "mongo-dev")
}

func TestDisconnectClosesConnectionThenNextExpandOpensANewOne(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	selectNode(t, tm, "pg-dev")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "hqdb")

	// Selection stays on pg-dev itself after expanding it, so D acts on
	// the Profile without any further navigation.
	// Disconnecting collapses the Profile; wait for that before expanding
	// again, or D and Enter can land in one frame that changes nothing.
	tm.Send(tea.KeyPressMsg{Code: 'D', Text: "D"})
	waitForScreen(t, tm, func(s string) bool { return !strings.Contains(s, "hqdb") })

	// Expanding again must succeed, proving the next expand opened a
	// fresh connection rather than reusing a closed one.
	enter(tm)
	waitForText(t, tm, "hqdb")
}

func TestDoubleClickExpandsSameAsEnter(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "my-dev")

	// my-dev is the 6th row (0-indexed 5) in the alphabetical profile
	// list, and nothing is expanded yet, so it renders at screen row 6,
	// below the tree pane's top border.
	tm.Send(tea.MouseClickMsg{X: 5, Y: 6, Button: tea.MouseLeft})
	tm.Send(tea.MouseClickMsg{X: 5, Y: 6, Button: tea.MouseLeft})
	waitForFrameContaining(t, tm.Output(), "hqdb")
}

func TestDDLTabOpensReconstructedPostgresTableDDL(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	selectNode(t, tm, "pg-dev")
	enter(tm) // pg-dev -> Database
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Schemas
	waitForFrameContaining(t, tm.Output(), "public")
	selectNode(t, tm, "public")
	enter(tm) // public -> Tables
	waitForFrameContaining(t, tm.Output(), "widgets  table")

	selectNode(t, tm, "widgets")
	frame := waitForFrameContaining(t, tm.Output(), "d ddl")
	if !bytes.Contains(frame, []byte("widgets  table")) {
		t.Fatalf("expected widgets still selected before pressing d:\n%s", frame)
	}

	tm.Send(tea.KeyPressMsg{Code: 'd', Text: "d"})
	frame = waitForFrameContaining(t, tm.Output(), "reconstructed")
	for _, want := range []string{"widgets", "price", "reconstructed"} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Errorf("DDL tab frame missing %q:\n%s", want, frame)
		}
	}
}

func TestDDLTabOpensNativeClickhouseTableDDL(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "ch-dev")

	enter(tm) // ch-dev sorts first: no navigation needed -> Databases
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Tables (clickhouse has no Schema level)
	waitForFrameContaining(t, tm.Output(), "widgets  table")

	selectNode(t, tm, "widgets")
	waitForFrameContaining(t, tm.Output(), "d ddl")

	tm.Send(tea.KeyPressMsg{Code: 'd', Text: "d"})
	frame := waitForFrameContaining(t, tm.Output(), "DDL ch-dev hqdb.widgets")
	if bytes.Contains(frame, []byte("reconstructed")) {
		t.Errorf("clickhouse DDL must be native, not labeled reconstructed:\n%s", frame)
	}
}

func TestDDLTabOpensViewDefinition(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	selectNode(t, tm, "pg-dev")
	enter(tm) // pg-dev -> Database
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Schemas
	waitForFrameContaining(t, tm.Output(), "public")
	selectNode(t, tm, "analytics")
	enter(tm) // analytics -> Tables
	// Not "  view" with the closing paren: at this depth (profile > db >
	// schema > table) the 34-column tree pane truncates the line right
	// before it, same as the comment on TestTableKindMarksViewDistinctlyFromTable
	// explains for "wview".
	waitForFrameContaining(t, tm.Output(), "cheap_widgets  view")

	selectNode(t, tm, "cheap_widgets")
	waitForFrameContaining(t, tm.Output(), "d ddl")

	tm.Send(tea.KeyPressMsg{Code: 'd', Text: "d"})
	frame := waitForFrameContaining(t, tm.Output(), "DDL pg-dev hqdb.analytics.cheap_widgets")
	if bytes.Contains(frame, []byte("reconstructed")) {
		t.Errorf("a view's DDL is always native, must not be labeled reconstructed:\n%s", frame)
	}
	if !bytes.Contains(frame, []byte("widgets")) {
		t.Errorf("view definition should mention the underlying table:\n%s", frame)
	}
}

func TestDDLKeyDoesNothingAndIsNotHintedOnARedisProfileNode(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "redis-dev")

	selectNode(t, tm, "redis-dev")
	frame := waitForFrameContaining(t, tm.Output(), "▌▸ redis-dev")
	if bytes.Contains(frame, []byte("d ddl")) {
		t.Errorf("status bar must not hint d on a redis node:\n%s", frame)
	}

	// "d" on a redis node is a true no-op (see TestDKeyOnSQLTableOpensADDLTab
	// and TestDKeyDoesNothingOnMongoCollectionViaIsDDLTable in
	// ddltab_model_test.go for the Model-level seam proving this): since it
	// changes nothing on screen, there is no new frame to wait for here.
	// Confirm the program is still alive and responsive by moving the
	// selection afterwards and observing a fresh frame.
	tm.Send(tea.KeyPressMsg{Code: 'd', Text: "d"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyUp})
	frame = waitForFrameContaining(t, tm.Output(), "▌▸ pg-")
	if bytes.Contains(frame, []byte("(DDL)")) {
		t.Errorf("d on a redis node must not have opened a DDL tab:\n%s", frame)
	}
}

func TestDDLTabCopyEmitsFullDefinitionViaOSC52(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	selectNode(t, tm, "pg-dev")
	enter(tm) // pg-dev -> Database
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Schemas
	waitForFrameContaining(t, tm.Output(), "public")
	selectNode(t, tm, "public")
	enter(tm) // public -> Tables
	waitForFrameContaining(t, tm.Output(), "widgets  table")
	selectNode(t, tm, "widgets")

	tm.Send(tea.KeyPressMsg{Code: 'd', Text: "d"})
	waitForFrameContaining(t, tm.Output(), "reconstructed")

	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab}) // move focus to the main pane
	tm.Send(tea.KeyPressMsg{Code: 'y', Text: "y"})

	// OSC52 ("\x1b]52;c;<base64>\x07") is written straight to the terminal
	// output by bubbletea's SetClipboard handling; its distinctive prefix
	// is a safe literal match even though the DDL body around it is
	// syntax-highlighted with its own ANSI codes.
	waitForFrame(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("\x1b]52;c;"))
	})
}

func TestMouseClickFocusesTreeOrTabArea(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)

	waitForFrameContaining(t, tm.Output(), "pg-dev")

	// Click inside the tab area (right of the fixed-width tree pane). The
	// tree stays on screen; what changes is the status bar losing the
	// tree's own hints, since focus moved away from it.
	tm.Send(tea.MouseClickMsg{X: 60, Y: 5, Button: tea.MouseLeft})
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "TABS") && !strings.Contains(s, "select up")
	})

	// Click back inside the tree: its hints reappear.
	tm.Send(tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	waitForFrameContaining(t, tm.Output(), "select up")
}

// upN sends the up arrow n times; it is used to walk a tree selection back
// to the very first row regardless of how deep it currently is, since
// selection can never go below index 0.
func upN(tm *teatest.TestModel, n int) {
	for i := 0; i < n; i++ {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyUp})
	}
}

func TestEnterOnSQLTableOpensDataViewWithFirstPage(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	selectNode(t, tm, "pg-dev")
	enter(tm) // pg-dev -> Database (also opens nothing: pg-dev itself is not a Table)
	waitForFrameContaining(t, tm.Output(), "hqdb")

	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Schemas
	waitForFrameContaining(t, tm.Output(), "public")

	selectNode(t, tm, "public")
	enter(tm) // public -> Tables
	waitForFrameContaining(t, tm.Output(), "widgets  table")

	selectNode(t, tm, "widgets")
	enter(tm) // widgets -> still expands to Columns in the tree AND opens a Data view tab

	// The tab title draws before the first page arrives, so wait for the
	// whole screen to show both.
	waitForScreen(t, tm, func(s string) bool {
		for _, want := range []string{"DATA pg-dev hqdb.public.widgets", "id", "name", "price", "1-3"} {
			if !strings.Contains(s, want) {
				return false
			}
		}
		return true
	})
}

func TestDataViewPagingPageSizeAndValueRendering(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	pgProfile, err := cfg.Profile("pg-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(pgProfile)
	if err != nil {
		t.Fatalf("connecting to pg-dev to seed a paging fixture: %v", err)
	}
	sqlConn := conn.(*adapters.SQL)
	// A per-run name, so a concurrent or leftover run never collides.
	table := uniqueName("tpg")
	if _, err := sqlConn.SQL().Exec(fmt.Sprintf(
		"CREATE TABLE public.%s (id serial primary key, val text, blob bytea)", table)); err != nil {
		conn.Close()
		t.Fatalf("seeding paging fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlConn.SQL().Exec("DROP TABLE IF EXISTS public." + table)
		conn.Close()
	})
	for i := 1; i <= 35; i++ {
		val := fmt.Sprintf("row%02d", i)
		var blob any
		switch i {
		case 1:
			val = "" // empty string, distinct from a NULL blob on the same row
		case 3:
			blob = []byte{0x00, 0xff, 0x10} // not valid UTF-8: forces the b64: form
		}
		if _, err := sqlConn.SQL().Exec(
			fmt.Sprintf("INSERT INTO public.%s (val, blob) VALUES ($1, $2)", table), val, blob); err != nil {
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
	enter(tm) // public -> Tables; selection stays on public
	waitForFrameContaining(t, tm.Output(), table+"  table")

	selectNode(t, tm, table)
	enter(tm) // -> opens the Data view
	frame := waitForFrameContaining(t, tm.Output(), "1-30")
	for _, want := range []string{"<NULL>", "b64:"} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Errorf("frame missing %q:\n%s", want, frame)
		}
	}

	// n pages forward to the last (partial) page.
	tm.Send(tea.KeyPressMsg{Code: 'n', Text: "n"})
	waitForFrameContaining(t, tm.Output(), "31-35")

	// p pages back to the first page.
	tm.Send(tea.KeyPressMsg{Code: 'p', Text: "p"})
	waitForFrameContaining(t, tm.Output(), "1-30")

	// s edits the page size; typing "5" and Enter reloads at that size.
	// The new indicator ("1-5") shares a common "1-" prefix with the
	// previous one ("1-30"), so a diffed terminal frame may only carry
	// the differing tail; "page size 5" is the part guaranteed to be
	// freshly (re)written since the whole trailing half of the line
	// shifts once "loading..." drops back out.
	tm.Send(tea.KeyPressMsg{Code: 's', Text: "s"})
	tm.Send(tea.KeyPressMsg{Code: '5', Text: "5"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForFrameContaining(t, tm.Output(), "page size 5")
}

func TestDataViewWhereOrderByAndCount(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	pgProfile, err := cfg.Profile("pg-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(pgProfile)
	if err != nil {
		t.Fatalf("connecting to pg-dev to seed a filter fixture: %v", err)
	}
	sqlConn := conn.(*adapters.SQL)
	table := uniqueName("tfl")
	if _, err := sqlConn.SQL().Exec("CREATE TABLE public." + table + " (id integer primary key, val text)"); err != nil {
		conn.Close()
		t.Fatalf("seeding filter fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlConn.SQL().Exec("DROP TABLE IF EXISTS public." + table)
		conn.Close()
	})
	for i := 1; i <= 40; i++ {
		if _, err := sqlConn.SQL().Exec("INSERT INTO public."+table+" (id, val) VALUES ($1, $2)", i, fmt.Sprintf("row%02d", i)); err != nil {
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
	waitForFrameContaining(t, tm.Output(), table+"  table")
	selectNode(t, tm, table)
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "1-30")

	// o sorts on the server: row40 is not on the unsorted first page.
	tm.Send(tea.KeyPressMsg{Code: 'o', Text: "o"})
	typeText(tm, "id DESC")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForFrameContaining(t, tm.Output(), "row40")

	// f filters on the server, keeping the sort; row01 is not on the
	// descending first page (row40..row11).
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	typeText(tm, "id < 3")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForFrameContaining(t, tm.Output(), "row01")

	// # counts the rows matching the current WHERE.
	tm.Send(tea.KeyPressMsg{Code: '#', Text: "#"})
	waitForText(t, tm, "of 2")

	// Changing the WHERE clears the stale total.
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	typeText(tm, "4")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	frame := waitForFrameContaining(t, tm.Output(), "row03")
	if bytes.Contains(frame, []byte("of 2")) {
		t.Errorf("stale total still shown after changing WHERE:\n%s", frame)
	}

	// An invalid WHERE shows the server error in the tab...
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	for range "id < 4" {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(tm, "nosuchcol = 1")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForFrameContaining(t, tm.Output(), "does not exist")

	// ...and the inputs stay editable: fixing the WHERE reloads.
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	for range "nosuchcol = 1" {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(tm, "id = 5")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForFrameContaining(t, tm.Output(), "row05")
}

func TestTabsAcrossProfilesSwitchCloseAndClickFocus(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "ch-dev")

	// ch-dev -> hqdb -> Tables -> widgets: open tab 0. ClickHouse lists
	// every database on the server, not just the one the Profile is
	// scoped to, so hqdb is not the first child: INFORMATION_SCHEMA,
	// default, then hqdb sort ahead of it.
	enter(tm) // ch-dev -> Databases; selection stays on ch-dev
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Tables ("widgets  table"); selection stays on hqdb
	waitForFrameContaining(t, tm.Output(), "widgets  table")
	selectNode(t, tm, "widgets")
	enter(tm) // -> opens the Data view tab
	waitForFrameContaining(t, tm.Output(), "DATA ch-dev hqdb.widgets")

	// Back to the tree, collapse ch-dev so the profile list is back to its
	// pristine order, then down to pg-dev -> hqdb -> analytics ->
	// widgets_extra: open tab 1. cheap_widgets (a view) sorts before
	// widgets_extra within analytics, both fixed fixtures untouched by any
	// other test.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	upN(tm, 20)
	enter(tm) // collapse ch-dev back to a single row
	selectNode(t, tm, "pg-dev")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Schemas; selection stays on hqdb
	waitForFrameContaining(t, tm.Output(), "analytics")
	selectNode(t, tm, "analytics")
	enter(tm) // analytics -> Tables; selection stays on analytics
	// The tree pane's fixed 34-column width clips "  table" off a name
	// this long at this depth, so "widgets_extra" (no kind suffix) is
	// what actually renders in full.
	waitForFrameContaining(t, tm.Output(), "widgets_extra")
	selectNode(t, tm, "widgets_extra")
	enter(tm)
	// The tab title draws before the first page arrives, so wait for both.
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "DATA pg-dev hqdb.analytics.widgets_extra") && strings.Contains(s, "label")
	})

	// Click tab 0's bar region (the leftmost cell of the main pane, which
	// is always inside the first tab regardless of its title's length):
	// focuses it, and its own columns (ch-dev's, not widgets_extra's)
	// redraw. The tree pane is a fixed 34 columns wide (see treeWidth in
	// model.go), so x=34 is the first column of the main pane.
	tm.Send(tea.MouseClickMsg{X: 34, Y: 0, Button: tea.MouseLeft})
	frame := waitForFrameContaining(t, tm.Output(), "price")
	if !bytes.Contains(frame, []byte("DATA ch-dev hqdb.widgets")) {
		t.Errorf("clicking tab 0 did not focus it:\n%s", frame)
	}

	// ] switches to the next tab (tab 1).
	tm.Send(tea.KeyPressMsg{Code: ']', Text: "]"})
	waitForFrameContaining(t, tm.Output(), "label")

	// x closes the active tab (tab 1); only ch-dev's tab is left.
	tm.Send(tea.KeyPressMsg{Code: 'x', Text: "x"})
	frame = waitForFrameContaining(t, tm.Output(), "DATA ch-dev hqdb.widgets")
	if bytes.Contains(frame, []byte("widgets_extra")) {
		t.Errorf("closing the active tab should have removed it:\n%s", frame)
	}
}

// typeText sends one KeyPressMsg per rune of s, exactly as a user typing it
// into a focused field would.
func typeText(tm *teatest.TestModel, s string) {
	for _, r := range s {
		tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestTreeFilterNarrowsVisibleProfilesLocallyByName(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	tm.Send(tea.KeyPressMsg{Code: '/', Text: "/"})
	typeText(tm, "pg-dev")
	screen := waitForText(t, tm, "filter: pg-dev")
	for _, unwanted := range []string{"redis-dev", "mongo-dev", "ms-dev", "my-dev", "ch-dev"} {
		if strings.Contains(screen, unwanted) {
			t.Errorf("non-matching profile %q still shown while filtering:\n%s", unwanted, screen)
		}
	}
	if !strings.Contains(screen, "pg-dev  postgres") {
		t.Errorf("filtered screen missing the one match:\n%s", screen)
	}

	// Clearing the query (backspacing it empty) restores the full tree.
	// (Esc leaving the field without discarding the query is covered by
	// TestEscLeavesFilterFieldWithoutClearingQuery, which does not need a
	// real terminal to observe it.)
	for range "pg-dev" {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	waitForFrameContaining(t, tm.Output(), "redis-dev")
}

func TestTreeFilterFindsTableBeyondLoadedChunkViaServerSearch(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	pgProfile, err := cfg.Profile("pg-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(pgProfile)
	if err != nil {
		t.Fatalf("connecting to pg-dev to seed a table: %v", err)
	}
	sqlConn := conn.(*adapters.SQL)
	// "zzft<n>" sorts after every table the fixture or another concurrent
	// test could plausibly create in "public" (including the permanent
	// "widgets"), so a chunk size of 1 is guaranteed to load some other
	// table and leave this one only reachable by name. Kept short (the
	// tree pane is a fixed 34 columns, and a Table row at this depth
	// leaves little room - see TestTableKindMarksViewDistinctlyFromTable).
	target := fmt.Sprintf("zzft%06d", time.Now().UnixNano()%1000000)
	if _, err := sqlConn.SQL().Exec("CREATE TABLE public." + target + " (id int)"); err != nil {
		conn.Close()
		t.Fatalf("seeding table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlConn.SQL().Exec("DROP TABLE IF EXISTS public." + target)
		conn.Close()
	})

	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")

	selectNode(t, tm, "pg-dev")
	enter(tm) // pg-dev -> Database
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm) // hqdb -> Schemas
	waitForFrameContaining(t, tm.Output(), "public")
	selectNode(t, tm, "public")

	// Only the "public" -> Tables fetch that follows must run on a
	// shrunk chunk; the levels already loaded above used the real one.
	restore := ui.WithCatalogChunk(1)
	t.Cleanup(restore)
	enter(tm) // public -> Tables, truncated to its first table
	frame := waitForFrameContaining(t, tm.Output(), "load more")
	if bytes.Contains(frame, []byte(target)) {
		t.Fatalf("target table already in the loaded chunk, test is not exercising the search path:\n%s", frame)
	}

	tm.Send(tea.KeyPressMsg{Code: '/', Text: "/"})
	typeText(tm, target)
	// The target's row appearing proves the server-side search found it:
	// it was absent from the loaded chunk just above, and the local
	// substring match alone cannot see a node that was never loaded. (The
	// status bar's own "filter active" indicator is covered character by
	// character as it is typed in TestTreeFilterNarrowsVisibleProfilesLocallyByName;
	// asserting it again here, after a long typed string plus an async
	// search response, would be redundant and sensitive to exactly which
	// lines the terminal renderer happens to repaint in full.)
	waitForFrameContaining(t, tm.Output(), target+"  table")
}

func TestDataViewCellPopupCopyAndSearch(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	pgProfile, err := cfg.Profile("pg-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(pgProfile)
	if err != nil {
		t.Fatalf("connecting to pg-dev to seed a cells fixture: %v", err)
	}
	sqlConn := conn.(*adapters.SQL)
	table := uniqueName("tcl")
	if _, err := sqlConn.SQL().Exec(fmt.Sprintf(
		"CREATE TABLE public.%s (id serial primary key, long text, doc jsonb)", table)); err != nil {
		conn.Close()
		t.Fatalf("seeding cells fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlConn.SQL().Exec("DROP TABLE IF EXISTS public." + table)
		conn.Close()
	})
	longVal := strings.Repeat("x", 80) + "TAILMARK"
	for _, r := range [][2]any{
		{longVal, `{"k":"v","n":[1]}`},
		{"needle one", nil},
		{"other", `{"needle":2}`},
	} {
		if _, err := sqlConn.SQL().Exec(
			fmt.Sprintf("INSERT INTO public.%s (long, doc) VALUES ($1, $2)", table), r[0], r[1]); err != nil {
			t.Fatalf("seed row: %v", err)
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
	waitForFrameContaining(t, tm.Output(), table+"  table")
	selectNode(t, tm, table)
	enter(tm)
	// Opening the Data view focuses it, and the status bar then lists the
	// grid's keys.
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "1-3") && strings.Contains(s, "enter cell popup") &&
			strings.Contains(s, "y copy") && strings.Contains(s, "/ search page")
	})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyRight}) // long column

	// Long value: the popup shows it in full; Esc closes it.
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "TAILMARK")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitForText(t, tm, "needle one") // the grid is drawn again over the popup

	// y copies the cell via OSC52; Y copies the row as JSON.
	tm.Send(tea.KeyPressMsg{Code: 'y', Text: "y"})
	waitForFrameContaining(t, tm.Output(), "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(longVal)))
	tm.Send(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	rowPrefix := `{"id":1,"long":"xx` // 18 bytes, so its base64 is a stable prefix
	waitForFrameContaining(t, tm.Output(), "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(rowPrefix)))

	// JSON value: pretty-printed in the popup.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyRight}) // doc column
	enter(tm)
	waitForFrameContaining(t, tm.Output(), `"k": "v"`)
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitForText(t, tm, "needle one") // the grid is drawn again over the popup

	// / searches the page and moves the cursor between matches.
	tm.Send(tea.KeyPressMsg{Code: '/', Text: "/"})
	typeText(tm, "needle")
	enter(tm)
	waitForText(t, tm, "(1/2)")
	tm.Send(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	waitForText(t, tm, "(2/2)")
}

// altEnter sends Alt+Enter, the Console's run key.
func altEnter(tm *teatest.TestModel) {
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
}

// runInConsole appends stmt on a new line of the focused Console editor
// and runs it with the cursor at its end.
func runInConsole(tm *teatest.TestModel, stmt string) {
	enter(tm)
	typeText(tm, stmt+";")
	altEnter(tm)
}

// confirmWrite answers "y" to the Console's write confirmation.
func confirmWrite(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	waitForFrameContaining(t, tm.Output(), "Run? y/N")
	tm.Send(tea.KeyPressMsg{Code: 'y', Text: "y"})
}

func TestSQLConsoleRunsStatementsOnItsOwnSession(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")
	selectNode(t, tm, "pg-dev")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	// The tab title names the Profile.
	waitForFrameContaining(t, tm.Output(), "pg-dev  SQL console")

	// More rows than the preview limit: the grid shows 500 with a marker.
	typeText(tm, "SELECT 'r' || g AS v FROM generate_series(1, 600) g;")
	altEnter(tm)
	waitForText(t, tm, "truncated at 500")

	// SET and a temporary table persist to the next statement. The temp
	// table dies with the Console's session, so it needs no cleanup.
	runInConsole(tm, "SET application_name = 'hqconsoleapp'")
	confirmWrite(t, tm)
	waitForText(t, tm, "0 rows affected")
	runInConsole(tm, "SHOW application_name")
	waitForText(t, tm, "hqconsoleapp")
	runInConsole(tm, "CREATE TEMP TABLE aaa_console_tmp (a int)")
	confirmWrite(t, tm)
	runInConsole(tm, "INSERT INTO aaa_console_tmp VALUES (1), (2), (3)")
	confirmWrite(t, tm)
	waitForText(t, tm, "3 rows affected")
	runInConsole(tm, "SELECT 'tmpok' || count(*) AS n FROM aaa_console_tmp")
	waitForText(t, tm, "tmpok3")

	// Export reruns the last statement on the same session, so it still
	// sees the temporary table.
	dest := filepath.Join(t.TempDir(), "console.csv")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	tm.Send(tea.KeyPressMsg{Code: 'e', Text: "e"})
	typeText(tm, dest)
	enter(tm)
	waitForText(t, tm, "1 rows written")
	if got, err := os.ReadFile(dest); err != nil || string(got) != "n\ntmpok3\n" {
		t.Fatalf("export file = %q, %v", got, err)
	}
	tm.Send(tea.KeyPressMsg{Code: 'i', Text: "i"})

	// A server error shows in the result area; the Console stays usable.
	runInConsole(tm, "SELEC 1")
	confirmWrite(t, tm) // not a known read, so it asks
	waitForText(t, tm, "syntax error")
	runInConsole(tm, "SELECT 'still' || 'here' AS s")
	waitForText(t, tm, "stillhere")

	// A second Console of the same Profile has its own session.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	typeText(tm, "SELECT 'app=' || current_setting('application_name') || '|' AS a;")
	altEnter(tm)
	waitForFrameContaining(t, tm.Output(), "app=|")
	runInConsole(tm, "SELECT * FROM aaa_console_tmp")
	waitForFrameContaining(t, tm.Output(), "does not exist")
}

func TestSQLConsoleConfirmsWritesBeforeRunning(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")
	selectNode(t, tm, "pg-dev")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitForFrameContaining(t, tm.Output(), "pg-dev  SQL console")

	// A read runs without a prompt.
	typeText(tm, "SELECT 'noprompt' || 1 AS v;")
	altEnter(tm)
	waitForFrameContaining(t, tm.Output(), "noprompt1")

	// A write prompts, naming the Profile and the statement; n cancels.
	// The temp table dies with the Console's session: no cleanup needed.
	runInConsole(tm, "CREATE TEMP TABLE aaa_cf (a int)")
	waitForText(t, tm, "This will write to pg-dev: CREATE TEMP TABLE aaa_cf (a int). Run? y/N")
	tm.Send(tea.KeyPressMsg{Code: 'n', Text: "n"})
	waitForFrameContaining(t, tm.Output(), "Cancelled")
	runInConsole(tm, "SELECT * FROM aaa_cf")
	waitForFrameContaining(t, tm.Output(), "does not exist")

	// DELETE prompts too, and Esc cancels.
	runInConsole(tm, "DELETE FROM aaa_cf")
	waitForText(t, tm, "This will write to pg-dev: DELETE FROM aaa_cf. Run? y/N")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitForFrameContaining(t, tm.Output(), "Cancelled")

	// y runs it.
	runInConsole(tm, "CREATE TEMP TABLE aaa_cf (a int)")
	waitForFrameContaining(t, tm.Output(), "Run? y/N")
	tm.Send(tea.KeyPressMsg{Code: 'y', Text: "y"})
	waitForFrameContaining(t, tm.Output(), "0 rows affected")
	runInConsole(tm, "SELECT 'confirmed' || count(*) AS n FROM aaa_cf")
	waitForFrameContaining(t, tm.Output(), "confirmed0")
}

// TestConsoleBufferAndHistoryPersistAcrossRestarts quits a TUI and starts
// a new one on the same (temporary) state directory: the Console's buffer
// comes back, and ctrl+up recalls a statement run in the earlier session.
func TestConsoleBufferAndHistoryPersistAcrossRestarts(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	stateDir := filepath.Join(t.TempDir(), "hq")
	// openConsole starts a TUI, opens a pg-dev Console, and waits for the
	// frame that draws it, which must also contain want.
	openConsole := func(want string) *teatest.TestModel {
		tm := teatest.NewTestModel(t, ui.New(cfg, ui.WithStateDir(stateDir)), teatest.WithInitialTermSize(120, 30))
		waitForFrameContaining(t, tm.Output(), "pg-dev")
		selectNode(t, tm, "pg-dev")
		tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
		waitForFrame(t, tm.Output(), func(b []byte) bool {
			return bytes.Contains(b, []byte("pg-dev  SQL console")) && bytes.Contains(b, []byte(want))
		})
		return tm
	}

	first := openConsole("")
	typeText(first, "SELECT 'hist' || 'one' AS h;")
	altEnter(first)
	waitForFrameContaining(t, first.Output(), "histone")
	// An unexecuted line stays in the buffer but not in the history.
	enter(first)
	typeText(first, "-- kept draft")
	// Typed runes redraw piecemeal; leaving the editor redraws the header
	// once every key before it was handled.
	first.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitForFrameContaining(t, first.Output(), "results")
	first.Quit()
	first.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	second := openConsole("kept draft") // the restored buffer
	t.Cleanup(func() { second.Quit() })

	// Recall replaces the buffer with the SELECT alone, ready to rerun.
	second.Send(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl})
	waitForScreen(t, second, func(s string) bool {
		return strings.Contains(s, "history 1/1") && strings.Contains(s, "hist")
	})
	altEnter(second)
	waitForFrameContaining(t, second.Output(), "histone")
}

func TestSQLConsoleRefusesWritesOnAReadOnlyProfile(t *testing.T) {
	cfg := loadFixtureConfigAs(t, config.ReadOnly, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "pg-dev")
	selectNode(t, tm, "pg-dev")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitForFrameContaining(t, tm.Output(), "pg-dev  SQL console")

	// A write is refused outright, with the MCP server's reason, and never
	// reaches the database.
	runInConsole(tm, "CREATE TEMP TABLE aaa_ro (a int)")
	waitForText(t, tm, "Refused (read-only profile)")
	runInConsole(tm, "SELECT * FROM aaa_ro")
	waitForFrameContaining(t, tm.Output(), "does not exist")

	// A read still runs.
	runInConsole(tm, "SELECT 'roread' || 1 AS v")
	waitForFrameContaining(t, tm.Output(), "roread1")
}
