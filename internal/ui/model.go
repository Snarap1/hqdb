// Package ui is the hqdb terminal UI: a Bubble Tea v2 application that reads
// the same hqdb config as the MCP server and lets a human browse its
// Profiles by hand. It imports the core packages (config, adapters,
// catalog, ...) but never internal/server; see ADR-0003 and ADR-0002.
package ui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/consolestate"
)

// doubleClickWindow is how close two clicks on the same cell must land in
// time to count as a double-click. Bubble Tea v2 reports clicks one at a
// time, so double-click detection is our own small component (ADR-0002):
// we remember the last click and compare.
const doubleClickWindow = 500 * time.Millisecond

// pane identifies which area of the screen currently has keyboard focus.
type pane int

const (
	paneTree pane = iota
	paneMain
)

// statusBarHeight is the number of rows the status bar occupies at the
// bottom of the screen.
const statusBarHeight = 1

// treeWidth is the fixed width of the tree pane, border included, when it
// is visible.
const treeWidth = 34

// tabBarHeight is the tab bar's single row above the framed tab content.
const tabBarHeight = 1

// framePadX is the blank column between the tab frame's side borders and
// the tab's content.
const framePadX = 1

// Model is the root Bubble Tea model for hqdb: the Catalog tree on the left,
// a tab area on the right, and a status bar with hints for the focused
// pane. It never opens a connection on its own.
type Model struct {
	cfg  *config.Config
	keys keyMap
	pool *connPool
	// state persists Console buffers and history; nil (the default)
	// persists nothing, so only main opts in with WithStateDir.
	state *consolestate.Store

	tree catalogTree

	// tabs is the tab area to the right of the tree; activeTab is the
	// index of the one currently shown and receiving keys. [ and ]
	// cycle it, x closes it, and clicking a tab's own bar region selects
	// it (see handleMainClick).
	tabs      []tab
	activeTab int

	focus      pane
	treeHidden bool
	showHelp   bool
	// helpScroll is the help overlay's first shown line.
	helpScroll int

	// notice is a transient status bar message (see noticeMsg), cleared
	// by a tick noticeTTL after it was set; noticeSeq tells the tick for
	// the current notice from a stale one.
	notice    string
	noticeSeq int
	// spinning is set while a spinner tick is scheduled (see armSpinner).
	spinning bool
	// quitAsk is set while "q" waits for "y" because work is running.
	quitAsk bool

	width, height int

	// lastClick tracks the previous mouse click for our own double-click
	// detection (see doubleClickWindow).
	lastClickAt time.Time
	lastClickX  int
	lastClickY  int
}

// New builds the root model from an already-loaded config. Call
// config.Load before this; New itself never touches the config files or
// any Profile's server - a Profile connects only when its tree node is
// first expanded.
func New(cfg *config.Config, opts ...Option) Model {
	m := Model{cfg: cfg, keys: defaultKeyMap(), pool: newConnPool(cfg), tree: newCatalogTree(cfg), focus: paneTree}
	m.tree = m.tree.selectProfile(cfg.DefaultProfile)
	for _, o := range opts {
		o(&m)
	}
	return m
}

// Option configures New.
type Option func(*Model)

// WithStateDir persists each Profile's Console buffer and statement
// history under dir (main passes consolestate.DefaultDir()). Without it
// nothing is persisted.
func WithStateDir(dir string) Option {
	return func(m *Model) { m.state = consolestate.New(dir) }
}

// Init starts the program with no commands: the tree is already built from
// cfg, and no Profile is connected until a node is expanded.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles msg, then reports a reconnect the work behind msg made
// as a notice, and keeps the spinner ticking while any tab is busy.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	mm := next.(Model)
	var cmds []tea.Cmd
	if r, ok := msg.(reconnecter); ok && r.reconnected() != "" {
		cmds = append(cmds, mm.setNotice("reconnected to "+r.reconnected()))
	}
	if tick := mm.armSpinner(); tick != nil {
		cmds = append(cmds, tick)
	}
	if len(cmds) == 0 {
		return mm, cmd
	}
	return mm, tea.Batch(append(cmds, cmd)...)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.MouseClickMsg:
		return m.handleClick(msg.Mouse())

	case tea.MouseWheelMsg:
		return m.handleWheel(msg.Mouse())

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case noticeMsg:
		cmd := m.setNotice(msg.text)
		return m, cmd

	case noticeClearMsg:
		if msg.seq == m.noticeSeq {
			m.notice = ""
		}
		return m, nil

	case spinMsg:
		m.spinning = false
		return m, nil

	case tea.PasteMsg:
		return m.handlePaste(msg.Content)

	case catalogMsg:
		applyCatalogMsg(msg)
		return m, nil

	case redisKeyMsg:
		applyRedisKeyMsg(msg)
		return m, nil

	case mongoDataViewMsg:
		applyMongoDataViewMsg(msg)
		return m, nil

	case mongoCountMsg:
		applyMongoCountMsg(msg)
		return m, nil

	case mongoExportMsg:
		cmd := m.noticeIf(applyMongoExportMsg(msg))
		return m, cmd

	case filterSearchMsg:
		m.tree = applyFilterSearchMsg(m.tree, msg)
		return m, nil

	case pageViewMsg:
		applyPageViewMsg(msg)
		return m, nil

	case countViewMsg:
		applyCountViewMsg(msg)
		return m, nil

	case exportViewMsg:
		cmd := m.noticeIf(applyExportViewMsg(msg))
		return m, cmd

	case ddlMsg:
		applyDDLMsg(msg)
		return m, nil

	case mongoConsoleMsg:
		applyMongoConsoleMsg(msg)
		return m, nil

	case consoleMsg:
		applyConsoleMsg(msg)
		return m, nil

	case consoleExportMsg:
		cmd := m.noticeIf(applyConsoleExportMsg(msg))
		return m, cmd

	case redisConsoleMsg:
		applyRedisConsoleMsg(msg)
		return m, nil
	}
	return m, nil
}

// opensData reports whether activating n (Enter or a double-click) opens
// its Data view rather than expanding it: a Table (or Collection) or a
// redis key.
func opensData(n *node) bool {
	return n != nil && (n.kind == kindTable || n.kind == kindKey)
}

// openDataView opens the Data view tab for n (a Table or, for mongodb, a
// Collection, or a redis key) and focuses it (spec: "Enter or
// double-click on a Table to open its Data view"). When a tab for the
// same Profile and object is already open, it focuses that one instead of
// opening a duplicate. It is a no-op for any other node.
func (m Model) openDataView(n *node) (Model, tea.Cmd) {
	if !opensData(n) {
		return m, nil
	}
	a, ok := adapterUIs[n.adapter]
	if !ok {
		return m, nil
	}
	t, cmd := a.openData(m.pool, n)
	if !m.focusExisting(t) {
		m.bindAccess(t, n.profile)
		m = m.addTab(t)
	} else {
		cmd = nil
	}
	m.focus = paneMain
	return m, cmd
}

// focusExisting makes the open tab with fresh's Heading (its kind, Profile,
// and object) the active one and reports whether there was one; fresh,
// never shown, then has its work cancelled. Consoles are never deduped:
// two Consoles on one Profile are two sessions.
func (m *Model) focusExisting(fresh tab) bool {
	h := fresh.Heading()
	for i, t := range m.tabs {
		if t.Heading() == h {
			if c, ok := fresh.(tabCanceller); ok {
				c.Cancel()
			}
			m.activeTab = i
			return true
		}
	}
	return false
}

// addTab appends t and makes it the active tab.
func (m Model) addTab(t tab) Model {
	m.tabs = append(m.tabs, t)
	m.activeTab = len(m.tabs) - 1
	return m
}

// activeTabOrNil returns the currently active tab, or nil when no tabs are
// open or activeTab is out of range.
func (m Model) activeTabOrNil() tab {
	if m.activeTab < 0 || m.activeTab >= len(m.tabs) {
		return nil
	}
	return m.tabs[m.activeTab]
}

// prevTab and nextTab cycle the active tab; both are no-ops with zero or
// one tab open.
func (m Model) prevTab() Model {
	if len(m.tabs) == 0 {
		return m
	}
	m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
	return m
}

func (m Model) nextTab() Model {
	if len(m.tabs) == 0 {
		return m
	}
	m.activeTab = (m.activeTab + 1) % len(m.tabs)
	return m
}

// closeActiveTab removes the active tab and clamps the active index onto
// what remains; a no-op with no tabs open.
func (m Model) closeActiveTab() Model {
	if len(m.tabs) == 0 {
		return m
	}
	if c, ok := m.tabs[m.activeTab].(tabCanceller); ok {
		c.Cancel()
	}
	if c, ok := m.tabs[m.activeTab].(tabCloser); ok {
		c.Close()
	}
	rest := make([]tab, 0, len(m.tabs)-1)
	rest = append(rest, m.tabs[:m.activeTab]...)
	rest = append(rest, m.tabs[m.activeTab+1:]...)
	m.tabs = rest
	if m.activeTab >= len(m.tabs) {
		m.activeTab = len(m.tabs) - 1
	}
	return m
}

// cancelFocused cancels the running work of the focused pane: the active
// tab's, or the Catalog tree's loads.
func (m Model) cancelFocused() {
	if m.focus == paneTree {
		m.tree.work.cancelAll()
		return
	}
	if c, ok := m.activeTabOrNil().(tabCanceller); ok {
		c.Cancel()
	}
}

// tabCloser is implemented by a tab holding a resource of its own (a SQL
// Console's pinned session) to release when the tab closes.
type tabCloser interface {
	Close()
}

// consoleProfile returns the Profile a Console opened from n would be
// bound to, or "" when n's Profile has no Console (a broken Profile).
func consoleProfile(n *node) string {
	if n == nil {
		return ""
	}
	root := n.profileRoot()
	if root == nil || root.brokenMessage() != "" {
		return ""
	}
	if _, ok := adapterUIs[root.adapter]; ok {
		return root.profile
	}
	return ""
}

// openConsole opens a Console bound to n's Profile as a new active,
// focused tab (spec: "c on a Profile opens a Console bound to that
// Profile"); a no-op when that Profile has no Console. A mongodb Console
// starts on the Database n is in (spec story 85), else the Profile's; a
// redis Profile gets a Redis Console.
func (m Model) openConsole(n *node) Model {
	profile := consoleProfile(n)
	if profile == "" {
		return m
	}
	t := adapterUIs[n.profileRoot().adapter].openConsole(n, profile, m.state)
	m.bindAccess(t, profile)
	m = m.addTab(t)
	m.focus = paneMain
	return m
}

// bindAccess hands t its Profile's access mode when t runs typed text
// (a Console, a SQL Data view's WHERE and ORDER BY); see ADR-0003. A
// Profile whose access is invalid never loads, so its error is ignored
// here and t stays read-only.
func (m Model) bindAccess(t tab, profile string) {
	if a, ok := t.(interface{ setAccess(config.Access) }); ok {
		access, _ := config.ProfileAccess(m.cfg.Profiles[profile])
		a.setAccess(access)
	}
}

// openDDLIfTable opens a DDL tab when n is a Table with DDL (every SQL
// Adapter; not redis or mongodb, which have no DDL concept and no key
// bound to it either - see Model.ddlAvailable), as a new active tab
// (spec: "d on a Table opens its DDL in a tab"), or focuses the Table's
// DDL tab when one is already open.
func (m *Model) openDDLIfTable(n *node) tea.Cmd {
	if n == nil || !n.isDDLTable() {
		return nil
	}
	t, cmd := openDDLCmd(m.pool, n.profile, n.adapter, n.path)
	if m.focusExisting(t) {
		return nil
	}
	m.bindAccess(t, n.profile)
	*m = m.addTab(t)
	return cmd
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C cancels the focused pane's running work and never quits,
	// whatever has keyboard focus (even a text field).
	if key.Matches(msg, m.keys.Cancel) {
		m.cancelFocused()
		return m, nil
	}

	// A pending quit question takes the next key: "y" quits, any other
	// key keeps the program running.
	if m.quitAsk {
		m.quitAsk = false
		if msg.String() == "y" {
			return m, tea.Quit
		}
		return m, nil
	}

	// The help overlay is modal: it scrolls, and closes on Esc, ?, or q.
	if m.showHelp {
		return m.updateHelp(msg), nil
	}

	// alt+1..9 jumps to a tab from anywhere, a text field included, since
	// no field takes alt+digit as text.
	if i, ok := m.keys.jumpTabIndex(msg); ok {
		if i < len(m.tabs) {
			m.activeTab, m.focus = i, paneMain
		}
		return m, nil
	}

	// While the tree's name filter has focus, every key - including the
	// single letters that otherwise quit, toggle the tree, or open help -
	// must go into the field instead of acting; Esc itself is handled
	// inside catalogTree.updateFilter, which leaves the field without
	// clearing the typed query.
	if m.focus == paneTree && m.tree.filtering {
		var cmd tea.Cmd
		m.tree, cmd = m.tree.Update(msg, m.keys, m.pool)
		return m, cmd
	}

	if msg.String() == "esc" {
		if t := m.activeTabOrNil(); t != nil {
			t.Update(msg, m.keys, m.pool) // let a tab leave its own text field
		}
		return m, nil
	}

	inTextField := m.focus == paneMain
	if t := m.activeTabOrNil(); inTextField && (t == nil || !t.EditingText()) {
		inTextField = false
	}

	if !inTextField {
		switch {
		case key.Matches(msg, m.keys.Quit):
			if m.anyBusy() {
				m.quitAsk = true
				return m, nil
			}
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.showHelp, m.helpScroll = true, 0
			return m, nil
		case key.Matches(msg, m.keys.ToggleTree):
			m.treeHidden = !m.treeHidden
			if m.treeHidden {
				m.focus = paneMain
			}
			return m, nil
		case key.Matches(msg, m.keys.FocusNext), key.Matches(msg, m.keys.FocusPrev):
			m.focus = m.otherPane()
			return m, nil
		case key.Matches(msg, m.keys.PrevTab):
			return m.prevTab(), nil
		case key.Matches(msg, m.keys.NextTab):
			return m.nextTab(), nil
		case key.Matches(msg, m.keys.CloseTab):
			return m.closeActiveTab(), nil
		}
	}

	if m.focus == paneTree {
		if key.Matches(msg, m.keys.Console) {
			return m.openConsole(m.tree.selectedNode()), nil
		}
		if key.Matches(msg, m.keys.DDL) {
			sel := m.tree.selectedNode()
			return m, m.openDDLIfTable(sel)
		}
		// Enter on a Table (or Collection, or redis key) opens its Data
		// view; l / → is what lists a Table's Columns.
		if sel := m.tree.selectedNode(); key.Matches(msg, m.keys.Expand) && opensData(sel) {
			return m.openDataView(sel)
		}
		m.tree.pageRows = m.treeRows()
		var cmd tea.Cmd
		m.tree, cmd = m.tree.Update(msg, m.keys, m.pool)
		return m, cmd
	}

	if t := m.activeTabOrNil(); t != nil {
		// The cell popup is modal, but copying the value it shows is
		// what it offers ("y copy" in its footer).
		if c, ok := t.(copier); ok && popupOpen(t) && key.Matches(msg, m.keys.Copy) {
			return m.copy(c.CopyText, copiedNotice)
		}
		if !t.EditingText() {
			if c, ok := t.(copier); ok && key.Matches(msg, m.keys.Copy) {
				return m.copy(c.CopyText, copiedNotice)
			}
			if c, ok := t.(rowCopier); ok && key.Matches(msg, m.keys.CopyRow) {
				return m.copy(c.CopyRowText, func(string) string { return "copied row" })
			}
		}
		return m, t.Update(msg, m.keys, m.pool)
	}
	return m, nil
}

// updateHelp handles a key while the help overlay is open: the movement
// and paging keys scroll it, and Esc, ?, or q close it.
func (m Model) updateHelp(msg tea.KeyPressMsg) Model {
	page := max(m.height-6, 1)
	switch {
	case msg.String() == "esc", key.Matches(msg, m.keys.Help), key.Matches(msg, m.keys.Quit):
		m.showHelp = false
	case key.Matches(msg, m.keys.Up):
		m.helpScroll--
	case key.Matches(msg, m.keys.Down):
		m.helpScroll++
	case key.Matches(msg, m.keys.PageUp):
		m.helpScroll -= page
	case key.Matches(msg, m.keys.PageDown):
		m.helpScroll += page
	case key.Matches(msg, m.keys.Top):
		m.helpScroll = 0
	case key.Matches(msg, m.keys.Bottom):
		m.helpScroll = 1 << 20
	}
	m.helpScroll = max(m.helpScroll, 0) // renderHelp clamps the bottom
	return m
}

// treeRows is how many rows of the tree are on screen, for its paging
// keys.
func (m Model) treeRows() int {
	h := m.height - statusBarHeight - 2
	if m.tree.filtering {
		h--
	}
	return max(h, 1)
}

// copy copies what get returns via OSC52 and shows notice(text) for it;
// a no-op when there is nothing to copy yet (spec: "the copy action emits
// the full text via OSC52").
func (m Model) copy(get func() (string, bool), notice func(string) string) (tea.Model, tea.Cmd) {
	text, ok := get()
	if !ok {
		return m, nil
	}
	n := m.setNotice(notice(text))
	return m, tea.Batch(tea.SetClipboard(text), n)
}

// handlePaste delivers pasted text to the text field with focus: the
// tree's name filter, or the active tab's field when it implements paster.
func (m Model) handlePaste(text string) (tea.Model, tea.Cmd) {
	if m.focus == paneTree {
		if !m.tree.filtering {
			return m, nil
		}
		var cmd tea.Cmd
		m.tree, cmd = m.tree.pasteFilter(text, m.pool)
		return m, cmd
	}
	if p, ok := m.activeTabOrNil().(paster); ok {
		p.Paste(text)
	}
	return m, nil
}

// otherPane returns the pane focus should move to; with only two panes,
// forward and backward focus movement are the same operation. When the
// tree is hidden, the main pane is the only focusable pane.
func (m Model) otherPane() pane {
	if m.treeHidden {
		return paneMain
	}
	if m.focus == paneTree {
		return paneMain
	}
	return paneTree
}

// handleClick focuses whichever pane the click landed in; a click inside
// the tree also selects the row under the pointer, and a second click on
// that same row within doubleClickWindow activates it exactly like Enter
// (expand, collapse, extend a "load more" row, or open a Table's Data
// view). A click in the main pane either focuses a tab's own bar region or
// just focuses the pane. Clicks on the status bar change nothing.
func (m Model) handleClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if ms.Y >= m.height-statusBarHeight {
		return m, nil
	}
	if m.treeHidden || ms.X >= treeWidth {
		return m.handleMainClick(ms)
	}
	m.focus = paneTree

	isDouble := !m.lastClickAt.IsZero() && time.Since(m.lastClickAt) <= doubleClickWindow &&
		ms.X == m.lastClickX && ms.Y == m.lastClickY
	now := time.Now()
	if isDouble {
		now = time.Time{} // consumed: a third click starts a fresh pair, not another double
	}
	m.lastClickAt, m.lastClickX, m.lastClickY = now, ms.X, ms.Y

	// Rows sit inside the pane's border, below the filter line while one
	// is shown; a click on the border or the filter line only focuses.
	rowsTop, rowHeight := 1, m.height-statusBarHeight-2
	if m.tree.filtering {
		rowsTop++
		rowHeight--
	}
	if ms.Y < rowsTop || ms.Y >= rowsTop+max(rowHeight, 0) {
		m.lastClickAt = time.Time{}
		return m, nil
	}
	row := scrollTopFor(m.tree.selected, len(m.tree.visible()), rowHeight) + ms.Y - rowsTop
	m.tree = m.tree.selectRow(row)

	if !isDouble {
		return m, nil
	}
	if sel := m.tree.selectedNode(); opensData(sel) {
		return m.openDataView(sel)
	}
	var cmd tea.Cmd
	m.tree, cmd = m.tree.activate(m.pool)
	return m, cmd
}

// wheelStep is how many rows one mouse wheel notch moves.
const wheelStep = 3

// handleWheel scrolls the pane under the pointer by wheelStep rows (the
// tree's selection, or the active tab's cursor as its own up and down keys
// move it) without moving keyboard focus. A tab with a text field focused
// is left alone, so the wheel never edits text.
func (m Model) handleWheel(ms tea.Mouse) (tea.Model, tea.Cmd) {
	var dir int
	switch ms.Button {
	case tea.MouseWheelUp:
		dir = -1
	case tea.MouseWheelDown:
		dir = 1
	default:
		return m, nil
	}
	if ms.Y >= m.height-statusBarHeight || m.showHelp {
		return m, nil
	}
	if !m.treeHidden && ms.X < treeWidth {
		m.tree = m.tree.moveBy(dir * wheelStep)
		return m, nil
	}
	t := m.activeTabOrNil()
	if t == nil || t.EditingText() {
		return m, nil
	}
	code := tea.KeyDown
	if dir < 0 {
		code = tea.KeyUp
	}
	var cmds []tea.Cmd
	for range wheelStep {
		cmds = append(cmds, t.Update(tea.KeyPressMsg{Code: code}, m.keys, m.pool))
	}
	return m, tea.Batch(cmds...)
}

// handleMainClick focuses the main pane and, when the click landed on the
// tab bar's row, switches the active tab to whichever one is under it.
func (m Model) handleMainClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	m.focus = paneMain
	mainX := ms.X
	mainWidth := m.width
	if !m.treeHidden {
		mainX -= treeWidth
		mainWidth -= treeWidth
	}
	if ms.Y == 0 && len(m.tabs) > 0 {
		if i := tabAt(m.tabs, m.activeTab, mainWidth, mainX); i >= 0 {
			m.activeTab = i
		}
		m.lastClickAt = time.Time{}
		return m, nil
	}
	c, ok := m.activeTabOrNil().(cellClicker)
	if !ok {
		return m, nil
	}
	isDouble := !m.lastClickAt.IsZero() && time.Since(m.lastClickAt) <= doubleClickWindow &&
		ms.X == m.lastClickX && ms.Y == m.lastClickY
	now := time.Now()
	if isDouble {
		now = time.Time{}
	}
	m.lastClickAt, m.lastClickX, m.lastClickY = now, ms.X, ms.Y
	// Content sits inside the frame below the tab bar (see mainContent).
	w, h := mainWidth-2-2*framePadX, m.height-statusBarHeight-tabBarHeight-2
	x, y := mainX-1-framePadX, ms.Y-tabBarHeight-1
	if x < 0 || y < 0 || x >= w || y >= h {
		return m, nil
	}
	c.ClickCell(x, y, w, h, isDouble)
	return m, nil
}

// rowCopier is implemented by a tab that can copy its current row (the
// row copy key, "Y").
type rowCopier interface {
	CopyRowText() (string, bool)
}

// cellClicker is implemented by a tab with a grid, so a click in its
// content moves the cell cursor and a double-click opens the cell popup.
// x and y are relative to the tab's content area, sized width x height.
type cellClicker interface {
	ClickCell(x, y, width, height int, double bool)
}

// mainContent renders the tab area, width x height: the tab bar, then a
// frame holding the active tab's own content when any tabs are open,
// otherwise a broken Profile's or a failed fetch's full error for the
// selected tree node, so the user can read it without a popup, or else the
// empty placeholder.
func (m Model) mainContent(width, height int) string {
	bar := renderTabBar(m.tabs, m.activeTab, width)
	innerW, innerH := max(width-2-2*framePadX, 0), max(height-tabBarHeight-2, 0)
	focused := m.focus == paneMain
	var content string
	if t := m.activeTabOrNil(); t != nil {
		content = t.View(innerW, innerH, focused)
	} else {
		content = emptyHint(m.keys)
		if sel := m.tree.selectedNode(); sel != nil {
			if msg := sel.brokenMessage(); msg != "" {
				content = errorStyle.Width(innerW).Render(msg)
			} else if sel.fetchErr != nil {
				content = errorStyle.Width(innerW).Render(sel.fetchErrMessage())
			}
		}
	}
	content = lipgloss.NewStyle().PaddingLeft(framePadX).Render(content)
	return bar + "\n" + framePane(content, "", width, height-tabBarHeight, focused)
}

// paneHelp lists the focused pane's own key hints: the tree's while it is
// focused (with "d" only when the selected node is a Table with DDL), the
// active tab's own Help() while the main pane is focused and a tab is
// open, or none.
func (m Model) paneHelp() []key.Binding {
	if m.showHelp {
		return []key.Binding{hint("↑↓ pgup/pgdn", "scroll"), hint("esc", "close")}
	}
	if m.focus == paneTree {
		// DDL and Console go right after the open/expand keys so a
		// truncated hint line drops Refresh/Disconnect first.
		h := m.keys.treeHelp(m.tree.selectedNode())
		out := append([]key.Binding{}, h[:1]...)
		if m.ddlAvailable() {
			out = append(out, m.keys.DDL)
		}
		if consoleProfile(m.tree.selectedNode()) != "" {
			out = append(out, m.keys.Console)
		}
		return append(append(out, h[1:]...), m.keys.FocusNext)
	}
	if t := m.activeTabOrNil(); t != nil {
		if popupOpen(t) {
			return []key.Binding{m.keys.Copy, hint("esc", "close")}
		}
		return append(t.Help(m.keys), m.keys.FocusNext)
	}
	return []key.Binding{m.keys.FocusNext}
}

// hint is a status bar or help entry that is not a binding of its own.
func hint(keys, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys), key.WithHelp(keys, desc))
}

// emptyHint is the tab area's content while no tab is open: the few keys
// that get a session going.
func emptyHint(k keyMap) string {
	lines := []string{mutedStyle.Render("No tabs open."), ""}
	for _, b := range []key.Binding{
		hint("enter", "open a Table's Data view (on a Profile: expand)"),
		hint(k.Console.Help().Key, "open a Console on the selected Profile"),
		hint(k.Filter.Help().Key, "filter the tree by name"),
		hint(k.Help.Help().Key, "all keys"),
	} {
		lines = append(lines, "  "+accentStyle.Width(7).Render(b.Help().Key)+mutedStyle.Render(b.Help().Desc))
	}
	return strings.Join(lines, "\n")
}

// confirmer is implemented by a tab that can be waiting for a write
// confirmation's answer.
type confirmer interface {
	confirmPending() bool
}

// helpFirst names the help overlay group for what has focus, which the
// overlay shows first.
func (m Model) helpFirst() string {
	if m.focus == paneTree {
		return "TREE"
	}
	t := m.activeTabOrNil()
	if t == nil {
		return "TABS"
	}
	switch t.(type) {
	case *dataViewTab:
		return "DATA VIEW"
	case *mongoTab:
		return "MONGODB"
	case *ddlTab:
		return "DDL"
	case *redisKeyTab, *redisConsoleTab:
		return "REDIS"
	case *mongoConsoleTab:
		return "MONGODB"
	}
	return "CONSOLE"
}

// ddlAvailable reports whether the DDL action applies to the selected
// node, which gates both whether "d" does anything (handleKey) and whether
// the status bar hints it.
func (m Model) ddlAvailable() bool {
	n := m.tree.selectedNode()
	return n != nil && n.isDDLTable()
}

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	if m.width == 0 || m.height == 0 {
		v.SetContent("")
		return v
	}

	contentHeight := m.height - statusBarHeight
	if contentHeight < 0 {
		contentHeight = 0
	}

	var body string
	if m.treeHidden {
		body = m.mainContent(m.width, contentHeight)
	} else {
		mainWidth := max(m.width-treeWidth, 0)
		left := framePane(m.tree.View(treeWidth-2, contentHeight-2, m.focus == paneTree), "Profiles", treeWidth, contentHeight, m.focus == paneTree)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, m.mainContent(mainWidth, contentHeight))
	}

	sb := statusBar{mode: m.mode(), filter: m.tree.filterQuery, notice: m.notice, hints: m.paneHelp(), global: m.keys.globalHelp()}
	if m.quitAsk {
		sb.notice, sb.hints = "quit? a query/export is running - y quits", []key.Binding{hint("any other key", "keeps running")}
	}
	content := body + "\n" + sb.render(m.width)

	if m.showHelp {
		content = overlay(content, renderHelp(m.width, m.height-statusBarHeight, m.keys, m.helpFirst(), m.helpScroll), m.width, m.height)
	}

	v.SetContent(content)
	return v
}

// mode names what has the keyboard for the status bar's badge: the help
// overlay, the tree (or its filter field), or the active tab's kind, with
// INSERT while a Console's editor or a tab's text field takes the keys.
func (m Model) mode() string {
	switch {
	case m.quitAsk:
		return "QUIT"
	case m.showHelp:
		return "HELP"
	case m.focus == paneTree && m.tree.filtering:
		return "FILTER"
	case m.focus == paneTree:
		return "TREE"
	}
	t := m.activeTabOrNil()
	if t == nil {
		return "TABS"
	}
	if popupOpen(t) {
		return "CELL"
	}
	if c, ok := t.(confirmer); ok && c.confirmPending() {
		return "CONFIRM"
	}
	if t.EditingText() {
		return "INSERT"
	}
	return t.Heading().tag
}
