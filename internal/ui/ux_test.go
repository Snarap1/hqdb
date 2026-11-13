package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Snarap1/hqdb/internal/config"
)

func TestStatusBarKeepsGlobalHintsAtNarrowWidths(t *testing.T) {
	k := defaultKeyMap()
	for _, width := range []int{40, 60, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sb := statusBar{mode: "TREE", notice: "copied 12 chars", hints: k.treeHelp(nil), global: k.globalHelp()}
			bar := ansi.Strip(sb.render(width))
			if w := lipgloss.Width(bar); w != width {
				t.Errorf("bar width = %d, want %d", w, width)
			}
			if !strings.HasSuffix(bar, "? keys  q quit ") {
				t.Errorf("global hints missing at the right end: %q", bar)
			}
			if strings.Contains(bar, "select up") {
				t.Errorf("the self-evident arrow hints are back: %q", bar)
			}
		})
	}
}

func TestStatusBarShowsTheNoticeAfterTheBadge(t *testing.T) {
	sb := statusBar{mode: "DATA", notice: "exported 3 rows -> /tmp/x.csv", global: defaultKeyMap().globalHelp()}
	if bar := ansi.Strip(sb.render(100)); !strings.Contains(bar, "DATA   exported 3 rows -> /tmp/x.csv") {
		t.Errorf("notice missing: %q", bar)
	}
}

func TestNoticeClearsOnlyOnItsOwnTick(t *testing.T) {
	m := newTestModel()
	_ = m.setNotice("first")
	stale := m.noticeSeq
	_ = m.setNotice("second")
	next, _ := m.Update(noticeClearMsg{seq: stale})
	if got := next.(Model).notice; got != "second" {
		t.Errorf("a stale tick cleared the notice: %q", got)
	}
	next, _ = next.(Model).Update(noticeClearMsg{seq: m.noticeSeq})
	if got := next.(Model).notice; got != "" {
		t.Errorf("notice = %q after its own tick, want cleared", got)
	}
}

func TestCopyShowsANotice(t *testing.T) {
	tests := []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: 'y', Text: "y"}, "copied 4 chars"},
		{tea.KeyPressMsg{Code: 'Y', Text: "Y"}, "copied row"},
	}
	for _, tt := range tests {
		m := newTestModel()
		dv := newDataViewTab("alpha", "postgres", []string{"hqdb", "public", "widgets"})
		dv.grid = newGrid([]string{"name"}, [][]any{{"bolt"}})
		m.tabs, m.focus = []tab{dv}, paneMain
		next, cmd := m.handleKey(tt.key)
		if cmd == nil || next.(Model).notice != tt.want {
			t.Errorf("%s: notice = %q, want %q", tt.key, next.(Model).notice, tt.want)
		}
	}
}

func TestReconnectedWorkShowsANotice(t *testing.T) {
	m := newTestModel()
	dv := newDataViewTab("alpha", "postgres", []string{"hqdb", "public", "widgets"})
	m.tabs = []tab{dv}
	next, _ := m.Update(pageViewMsg{tab: dv, reconnect: reconnectedTo("alpha", true)})
	if got := next.(Model).notice; got != "reconnected to alpha" {
		t.Errorf("notice = %q, want the reconnect", got)
	}
}

func TestSpinnerTicksOnlyWhileATabIsBusy(t *testing.T) {
	m := newTestModel()
	if cmd := m.armSpinner(); cmd != nil {
		t.Error("an idle UI scheduled a spinner tick")
	}
	dv := newDataViewTab("alpha", "postgres", []string{"t"})
	dv.loading = true
	m.tabs = []tab{dv}
	if cmd := m.armSpinner(); cmd == nil || !m.spinning {
		t.Fatal("a busy tab did not start the spinner")
	}
	if cmd := m.armSpinner(); cmd != nil {
		t.Error("a second tick was scheduled while one is pending")
	}
	dv.loading = false
	next, cmd := m.Update(spinMsg{})
	if cmd != nil || next.(Model).spinning {
		t.Error("the spinner kept ticking once nothing was busy")
	}
}

func TestBusyTextShowsSpinnerAndElapsedTime(t *testing.T) {
	got := busyText("Running", time.Now().Add(-3200*time.Millisecond))
	if !strings.Contains(got, "Running 3.2s") || !strings.ContainsAny(got, string(spinnerFrames)) {
		t.Errorf("busyText = %q", got)
	}
	if got := busyText("loading", time.Time{}); got != "loading…" {
		t.Errorf("busyText with no start = %q", got)
	}
}

func TestQuitAsksWhileWorkIsRunning(t *testing.T) {
	q := tea.KeyPressMsg{Code: 'q', Text: "q"}
	tests := []struct {
		name     string
		busy     bool
		second   tea.KeyPressMsg
		wantQuit bool
	}{
		{"idle quits at once", false, tea.KeyPressMsg{}, true},
		{"busy then y quits", true, tea.KeyPressMsg{Code: 'y', Text: "y"}, true},
		{"busy then n keeps running", true, tea.KeyPressMsg{Code: 'n', Text: "n"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel()
			m.width, m.height = 100, 30
			dv := newDataViewTab("alpha", "postgres", []string{"t"})
			dv.loading = tt.busy
			m.tabs = []tab{dv}
			next, cmd := m.handleKey(q)
			if !tt.busy {
				if !isQuit(cmd) {
					t.Fatal("q did not quit an idle UI")
				}
				return
			}
			mm := next.(Model)
			if isQuit(cmd) || !mm.quitAsk {
				t.Fatal("q quit while work was running instead of asking")
			}
			if v := ansi.Strip(mm.View().Content); !strings.Contains(v, "quit? a query/export is running - y quits") {
				t.Errorf("status bar does not ask:\n%s", v)
			}
			next, cmd = mm.handleKey(tt.second)
			if isQuit(cmd) != tt.wantQuit || next.(Model).quitAsk {
				t.Errorf("answer %q: quit %v, want %v", tt.second.String(), isQuit(cmd), tt.wantQuit)
			}
		})
	}
}

func TestAltDigitJumpsToTab(t *testing.T) {
	m := newTestModel()
	m.tabs = manyTabs(4)
	next, _ := m.handleKey(tea.KeyPressMsg{Code: '3', Mod: tea.ModAlt})
	if mm := next.(Model); mm.activeTab != 2 || mm.focus != paneMain {
		t.Errorf("alt+3: activeTab %d focus %v, want 2 and the main pane", mm.activeTab, mm.focus)
	}
	next, _ = m.handleKey(tea.KeyPressMsg{Code: '9', Mod: tea.ModAlt})
	if mm := next.(Model); mm.activeTab != 0 {
		t.Errorf("alt+9 with 4 tabs moved to %d", mm.activeTab)
	}
}

func TestTreePagingAndEndsNeverWrap(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{}}
	for i := range 30 {
		cfg.Profiles[fmt.Sprintf("p%02d", i)] = map[string]any{"adapter": "postgres"}
	}
	m := New(cfg)
	m.width, m.height = 100, 12 // 9 tree rows
	tests := []struct {
		key  tea.KeyPressMsg
		from int
		want int
	}{
		{tea.KeyPressMsg{Code: tea.KeyPgDown}, 0, 9},
		{tea.KeyPressMsg{Code: tea.KeyPgDown}, 25, 29},
		{tea.KeyPressMsg{Code: tea.KeyPgUp}, 5, 0},
		{tea.KeyPressMsg{Code: 'G', Text: "G"}, 3, 29},
		{tea.KeyPressMsg{Code: tea.KeyEnd}, 3, 29},
		{tea.KeyPressMsg{Code: 'g', Text: "g"}, 20, 0},
		{tea.KeyPressMsg{Code: tea.KeyHome}, 20, 0},
		{tea.KeyPressMsg{Code: 'k', Text: "k"}, 0, 0},
		{tea.KeyPressMsg{Code: 'j', Text: "j"}, 29, 29},
	}
	for _, tt := range tests {
		m.tree.selected = tt.from
		next, _ := m.handleKey(tt.key)
		if got := next.(Model).tree.selected; got != tt.want {
			t.Errorf("%s from %d: selected %d, want %d", tt.key, tt.from, got, tt.want)
		}
	}
}

func TestGridPagingUsesTheShownRows(t *testing.T) {
	rows := make([][]any, 50)
	for i := range rows {
		rows[i] = []any{i}
	}
	g := newGrid([]string{"n"}, rows)
	_ = g.view(40, 11, true) // 10 body rows
	k := defaultKeyMap()
	tests := []struct {
		key  tea.KeyPressMsg
		from int
		want int
	}{
		{tea.KeyPressMsg{Code: tea.KeyPgDown}, 0, 10},
		{tea.KeyPressMsg{Code: tea.KeyPgDown}, 45, 49},
		{tea.KeyPressMsg{Code: tea.KeyPgUp}, 15, 5},
		{tea.KeyPressMsg{Code: 'G', Text: "G"}, 0, 49},
		{tea.KeyPressMsg{Code: 'g', Text: "g"}, 30, 0},
	}
	for _, tt := range tests {
		g.cursorRow = tt.from
		got, handled := g.update(tt.key, k)
		if !handled || got.cursorRow != tt.want {
			t.Errorf("%s from %d: row %d (handled %v), want %d", tt.key, tt.from, got.cursorRow, handled, tt.want)
		}
	}
}

func TestMouseWheelScrollsThePaneUnderThePointer(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 30
	rows := make([][]any, 20)
	for i := range rows {
		rows[i] = []any{i}
	}
	dv := newDataViewTab("alpha", "postgres", []string{"t"})
	dv.grid = newGrid([]string{"n"}, rows)
	m.tabs, m.focus = []tab{dv}, paneTree

	next, _ := m.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})
	if dv.grid.cursorRow != wheelStep || next.(Model).focus != paneTree {
		t.Errorf("wheel over the tab: row %d, focus %v; want row %d and focus unchanged", dv.grid.cursorRow, next.(Model).focus, wheelStep)
	}
	next, _ = next.(Model).Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown})
	if got := next.(Model).tree.selected; got != 1 { // two Profiles: clamped
		t.Errorf("wheel over the tree: selected %d, want 1", got)
	}
}

func TestDataViewNextPageStopsAtAShortPage(t *testing.T) {
	dv := newDataViewTab("alpha", "postgres", []string{"hqdb", "public", "widgets"})
	dv.grid = newGrid([]string{"n"}, [][]any{{1}, {2}})
	pool, _ := fakePool()
	if cmd := dv.Update(tea.KeyPressMsg{Code: 'n', Text: "n"}, defaultKeyMap(), pool); cmd != nil || dv.offset != 0 {
		t.Errorf("n past a short page loaded offset %d", dv.offset)
	}
}

func TestAccessBadgesInTreeAndTabHeaders(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]map[string]any{
		"ro-dev": {"adapter": "postgres"},
		"rw-dev": {"adapter": "postgres", "access": "read-write"},
	}}
	m := New(cfg)
	tree := strings.Split(ansi.Strip(m.tree.View(32, 10, true)), "\n")
	for i, want := range []string{"RO ", "RW "} {
		if !strings.HasSuffix(tree[i], want) || lipgloss.Width(tree[i]) != 32 {
			t.Errorf("tree row %q does not end in the %q badge", tree[i], want)
		}
	}
	for _, tt := range []struct {
		access config.Access
		badge  string
		text   string
	}{{config.ReadOnly, "RO", "read-only"}, {config.ReadWrite, "RW", "read-write"}} {
		c := newConsoleTab("pg", "", nil)
		c.setAccess(tt.access)
		v := ansi.Strip(c.View(120, 20, true))
		if !strings.Contains(v, "own session · autocommit · "+tt.text+"  "+tt.badge) {
			t.Errorf("%s Console header:\n%s", tt.access, v)
		}
		dv := newDataViewTab("pg", "postgres", []string{"t"})
		dv.setAccess(tt.access)
		if v := ansi.Strip(dv.View(100, 10, true)); !strings.Contains(v, "t  pg · postgres  "+tt.badge) {
			t.Errorf("%s Data view header:\n%s", tt.access, v)
		}
	}
}

func TestWriteConfirmPromptPunctuationAndReason(t *testing.T) {
	long := "UPDATE widgets SET name = 'a' WHERE id IN (1, 2, 3, 4, 5, 6, 7, 8, 9)"
	tests := []struct {
		name, stmt, reason string
		width              int
		want               []string
	}{
		{"short", "DELETE FROM t", "statement contains write keyword(s): DELETE", 80,
			[]string{"This will write to pg: DELETE FROM t. Run? y/N", "why: statement contains write keyword(s): DELETE"}},
		{"cut", long, "", 60, []string{"This will write to pg: UPDATE widgets SET name = '… Run? y/N"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c writeConfirm
			c.ask("pg", tt.stmt, tt.reason, func() tea.Cmd { return nil })
			got := c.prompt(tt.width)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("prompt = %q, want %q", got, tt.want)
			}
			if strings.Contains(got[0], "..") || ansi.StringWidth(got[0]) > tt.width {
				t.Errorf("prompt %q has double punctuation or overflows %d", got[0], tt.width)
			}
		})
	}
}

func TestReadWriteConsoleConfirmationShowsReasonAndKeys(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 120, 30
	c := newConsoleTab("alpha", "", nil)
	c.setAccess(config.ReadWrite)
	c.editor = editor{}.insert("DELETE FROM widgets")
	m.tabs, m.focus = []tab{c}, paneMain
	c.runCmd(nil)
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"This will write to alpha: DELETE FROM widgets. Run? y/N", "why: statement is not allowed; only SELECT", "CONFIRM", "y run  any other key cancels"} {
		if !strings.Contains(v, want) {
			t.Errorf("screen missing %q:\n%s", want, v)
		}
	}
}

func TestConsoleErrorWrapsAndOpensInThePopup(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 30
	c := newConsoleTab("alpha", "", nil)
	long := "Refused (read-only profile): statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE are; " + strings.Repeat("and more detail ", 20) + "END"
	c.err, c.editing = errors.New(long), false
	m.tabs, m.focus = []tab{c}, paneMain

	status := c.statusLines(60, func() string { return "" })
	if len(status) != maxStatusRows {
		t.Fatalf("status takes %d lines, want %d", len(status), maxStatusRows)
	}
	if !strings.Contains(ansi.Strip(status[1]), "SHOW") || !strings.HasSuffix(ansi.Strip(status[2]), "(enter: full error)") {
		t.Errorf("status = %q", status)
	}

	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm := next.(Model)
	if mm.mode() != "CELL" {
		t.Fatalf("mode = %q after Enter on an error, want CELL", mm.mode())
	}
	if v := ansi.Strip(mm.View().Content); !strings.Contains(v, "END") {
		t.Errorf("popup does not show the full error:\n%s", v)
	}
	_, cmd := mm.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if got := fmt.Sprint(clipboardMsgOf(cmd)); got != long {
		t.Errorf("copied %q, want the full error", got)
	}
}

func TestPopupSafeStripsControlCharacters(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"a\nb", "a\nb"},
		{"a\r\nb\rc", "a\nb\nc"},
		{"a\tb", "a   b"},
		{"\x1b[31mred\x1b[0m", "\ufffd[31mred\ufffd[0m"},
		{"bell\x07", "bell\ufffd"},
		{"c1\u009b2J", "c1\ufffd2J"},
		{"bad\xffutf8", "bad\ufffdutf8"},
	}
	for _, tt := range tests {
		if got := popupSafe(tt.in); got != tt.want {
			t.Errorf("popupSafe(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCellPopupNeverEmitsAValuesEscapeSequence(t *testing.T) {
	evil := "x\x1b]52;c;aGk=\x07y\x1b[2J"
	g := newGrid([]string{"v"}, [][]any{{evil}}).openPopupAt(0, 0)
	out := g.popupView(80, 20)
	if strings.Contains(out, "\x1b]52") || strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x07") {
		t.Errorf("popup passes the value's escape sequences through: %q", out)
	}
	if got, _ := g.cellCopyText(); got != evil {
		t.Errorf("copy = %q, want the raw value", got)
	}
}

func TestHelpKeyColumnFitsTheLongestKey(t *testing.T) {
	out := ansi.Strip(renderHelp(160, 80, defaultKeyMap(), "TREE", 0))
	if strings.Contains(out, "shift+enternew") {
		t.Errorf("key runs into its description:\n%s", out)
	}
	if !strings.Contains(out, "shift+enter") || !strings.Contains(out, "ctrl+u") {
		t.Errorf("help is missing editor keys:\n%s", out)
	}
}

func TestHelpShowsTheFocusedGroupFirstAndScrolls(t *testing.T) {
	k := defaultKeyMap()
	top := func(first string, scroll int) string {
		lines := strings.Split(ansi.Strip(renderHelp(60, 14, k, first, scroll)), "\n")
		return lines[3] // border, title, blank, then the first body line
	}
	if got := top("REDIS", 0); !strings.Contains(got, "REDIS") {
		t.Errorf("first group line = %q, want REDIS", got)
	}
	if got := top("REDIS", 1); strings.Contains(got, "REDIS") {
		t.Errorf("scrolled help still starts at the group title: %q", got)
	}
	if h := lipgloss.Height(renderHelp(60, 14, k, "TREE", 0)); h > 14 {
		t.Errorf("help is %d lines tall, want at most 14", h)
	}
	if out := ansi.Strip(renderHelp(60, 14, k, "TREE", 1<<20)); !strings.Contains(out, "of ") {
		t.Errorf("a clamped scroll lost the position note:\n%s", out)
	}
}

func TestDefaultProfileIsSelectedOnStart(t *testing.T) {
	cfg := &config.Config{DefaultProfile: "beta", Profiles: map[string]map[string]any{
		"alpha": {"adapter": "postgres"}, "beta": {"adapter": "postgres"}, "gamma": {"adapter": "redis"},
	}}
	if got := New(cfg).tree.selectedNode().name; got != "beta" {
		t.Errorf("selected %q on start, want the default_profile", got)
	}
}

func TestEmptyTabAreaHintsTheFirstKeys(t *testing.T) {
	m := newTestModel()
	m.tree.selected = 1 // a healthy Profile with no error to show
	out := ansi.Strip(m.mainContent(80, 20))
	for _, want := range []string{"No tabs open.", "enter", "/", "?"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty tab area missing %q:\n%s", want, out)
		}
	}
}
