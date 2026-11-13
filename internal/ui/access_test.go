package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/mongoshell"
)

func TestMongoViolation(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"find", "db.c.find()", ""},
		{"count", "db.c.countDocuments()", ""},
		{"reading aggregate", "db.c.aggregate([{$match: {}}])", ""},
		{"writing aggregate", "db.c.aggregate([{$out: 'x'}])", `aggregation stage "$out" writes data; this server permits reads only`},
		{"insertOne", "db.c.insertOne({a: 1})", `operation "insertOne" writes data; this server permits reads only`},
		{"insertMany", "db.c.insertMany([{a: 1}])", `operation "insertMany" writes data; this server permits reads only`},
		{"updateOne", "db.c.updateOne({}, {$set: {a: 1}})", `operation "updateOne" writes data; this server permits reads only`},
		{"deleteOne", "db.c.deleteOne({})", `operation "deleteOne" writes data; this server permits reads only`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd, err := mongoshell.ParseCommand(c.src)
			if err != nil {
				t.Fatal(err)
			}
			if got := mongoViolation(cmd.Request); got != c.want {
				t.Errorf("mongoViolation(%q) = %q, want %q", c.src, got, c.want)
			}
		})
	}
}

func TestConsoleAccess(t *testing.T) {
	cases := []struct {
		name      string
		access    config.Access
		violation string
		want      string // "run", "ask", or "refuse"
	}{
		{"a read runs on a read-only profile", config.ReadOnly, "", "run"},
		{"a write is refused on a read-only profile", config.ReadOnly, "DELETE is not allowed", "refuse"},
		{"a write asks on a read-write profile", config.ReadWrite, "DELETE is not allowed", "ask"},
		{"a read runs on a read-write profile", config.ReadWrite, "", "run"},
		{"the zero access is read-only", "", "DELETE is not allowed", "refuse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var core consoleCore
			core.profile = "p"
			core.setAccess(c.access)
			ran := false
			core.runOrAsk("stmt", c.violation, func() tea.Cmd { ran = true; return nil })
			got := "run"
			switch {
			case core.confirm.pending():
				got = "ask"
			case core.err != nil:
				got = "refuse"
			}
			if got != c.want || (got == "run") != ran {
				t.Fatalf("got %s (ran %v), want %s", got, ran, c.want)
			}
			if got == "refuse" && core.err.Error() != "Refused (read-only profile): "+c.violation {
				t.Errorf("refusal = %q", core.err)
			}
		})
	}
}

func TestReadOnlyConsoleRefusesAWriteInItsView(t *testing.T) {
	tb := newConsoleTab("pg-dev", "", nil)
	tb.editor = editor{}.insert("DROP TABLE t")
	tb.runCmd(nil)
	v := ansi.Strip(tb.View(120, 30, true))
	if !strings.Contains(v, "Refused (read-only profile)") || strings.Contains(v, "Run? y/N") {
		t.Errorf("view must show the refusal, not a confirmation:\n%s", v)
	}
}

func TestConsoleTakesItsProfilesAccess(t *testing.T) {
	for _, access := range []config.Access{config.ReadOnly, config.ReadWrite} {
		cfg := &config.Config{Profiles: map[string]map[string]any{
			"pg-dev": {"adapter": "postgres", "access": string(access)},
		}}
		m := New(cfg)
		m.width, m.height = 100, 30
		newM, _ := m.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
		ct := newM.(Model).tabs[0].(*consoleTab)
		if ct.access != access {
			t.Errorf("Console access = %q, want %q", ct.access, access)
		}
	}
}

func TestReadOnlyDataViewRefusesAWritingWhere(t *testing.T) {
	cases := []struct {
		name    string
		access  config.Access
		where   string
		orderBy string
		refused bool
	}{
		{"a stacked delete in WHERE", config.ReadOnly, "1=1; DELETE FROM widgets", "", true},
		{"a write keyword in ORDER BY", config.ReadOnly, "", "id; DROP TABLE widgets", true},
		{"a plain filter", config.ReadOnly, "price < 2 AND name <> 'drop'", "id DESC", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newDataViewTab("pg-dev", "postgres", []string{"hqdb", "public", "widgets"})
			d.setAccess(c.access)
			d.where, d.orderBy = c.where, c.orderBy
			err := d.screen()
			if (err != nil) != c.refused {
				t.Fatalf("screen() = %v, refused want %v", err, c.refused)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "Refused (read-only profile): ") {
				t.Errorf("refusal = %q", err)
			}
		})
	}
}

func TestReadWriteDataViewRunsAnyWhere(t *testing.T) {
	d := newDataViewTab("pg-dev", "postgres", []string{"hqdb", "public", "widgets"})
	d.setAccess(config.ReadWrite)
	d.where = "1=1; DELETE FROM widgets"
	if err := d.screen(); err != nil {
		t.Errorf("screen() = %v on a read-write Profile", err)
	}
}

func TestReadOnlyDataViewLoadNeverReachesTheDatabase(t *testing.T) {
	d := newDataViewTab("pg-dev", "postgres", []string{"hqdb", "public", "widgets"})
	d.where = "1=1; DELETE FROM widgets"
	// A nil pool panics if the load tries to connect.
	msg := d.loadCmd(nil)().(pageViewMsg)
	if msg.err == nil || !strings.HasPrefix(msg.err.Error(), "Refused (read-only profile): ") {
		t.Errorf("load err = %v, want the refusal", msg.err)
	}
	cmsg := d.countCmd(nil)().(countViewMsg)
	if cmsg.err == nil {
		t.Error("count must be refused too")
	}
	if d.exportCmd(nil, filepath.Join(t.TempDir(), "x.csv")) != nil || !strings.Contains(d.exportStatus, "Refused (read-only profile)") {
		t.Errorf("export status = %q, want the refusal", d.exportStatus)
	}
}

// Export reruns the Console's last statement, so a statement that writes
// (`DELETE ... RETURNING *` shows rows) must never be exported: rerunning
// it would write a second time, unconfirmed.
func TestConsoleExportNeverRerunsAWrite(t *testing.T) {
	const want = "export refused: export reruns the statement, which writes (" // + violation + ")"
	cases := []struct {
		name    string
		access  config.Access
		stmt    string
		refused bool
	}{
		{"a returning delete on a read-write profile", config.ReadWrite, "DELETE FROM widgets RETURNING *", true},
		{"a returning insert on a read-write profile", config.ReadWrite, "INSERT INTO widgets (name) VALUES ('x') RETURNING id", true},
		{"a returning update on a read-only profile", config.ReadOnly, "UPDATE widgets SET price = 0 RETURNING *", true},
		{"a select for update on a read-write profile", config.ReadWrite, "SELECT * FROM widgets FOR UPDATE", true},
		{"a plain select on a read-write profile", config.ReadWrite, "SELECT * FROM widgets", false},
		{"a plain select on a read-only profile", config.ReadOnly, "SELECT * FROM widgets", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tb := newConsoleTab("pg-dev", "", nil)
			tb.setAccess(c.access)
			tb.hasGrid, tb.lastStmt = true, c.stmt
			cmd := tb.exportCmd(nil, filepath.Join(t.TempDir(), "x.csv"))
			if c.refused {
				// A nil pool panics if the export tries to run.
				if cmd != nil {
					t.Fatal("export of a writing statement returned a command")
				}
				if !strings.HasPrefix(tb.exportStatus, want) || tb.confirm.pending() {
					t.Errorf("export status = %q (confirm pending %v), want the refusal", tb.exportStatus, tb.confirm.pending())
				}
				return
			}
			if cmd == nil || tb.exportStatus != "exporting..." {
				t.Errorf("export of a read: cmd %v, status %q", cmd != nil, tb.exportStatus)
			}
			tb.Cancel()
		})
	}
}

func TestConsoleExportKeyRefusesAWriteBeforeAskingForAPath(t *testing.T) {
	tb := newConsoleTab("pg-dev", "", nil)
	tb.setAccess(config.ReadWrite)
	tb.editing = false
	tb.hasGrid, tb.lastStmt = true, "DELETE FROM widgets RETURNING *"
	tb.grid = newGrid([]string{"id"}, [][]any{{1}})
	tb.Update(tea.KeyPressMsg{Code: 'e', Text: "e"}, defaultKeyMap(), nil)
	if tb.exporting {
		t.Fatal("the export path field opened for a writing statement")
	}
	if v := ansi.Strip(tb.View(160, 30, true)); !strings.Contains(v, "export refused: export reruns the statement, which writes") {
		t.Errorf("view must show the export refusal:\n%s", v)
	}
}

func TestDataViewTakesItsProfilesAccess(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"pg-dev": {"adapter": "postgres", "access": "read-write"},
	}}
	m := New(cfg)
	n := &node{kind: kindTable, profile: "pg-dev", adapter: "postgres", path: []string{"hqdb", "public", "widgets"}}
	m, _ = m.openDataView(n)
	if d := m.tabs[0].(*dataViewTab); d.access != config.ReadWrite {
		t.Errorf("Data view access = %q, want read-write", d.access)
	}
}
