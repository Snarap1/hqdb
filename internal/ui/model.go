// Package ui is the hqdb terminal UI: a Bubble Tea v2 application that reads
// the same hqdb config as the MCP server and lets a human browse its
// Profiles by hand. It imports the core packages (config, adapters,
// catalog, ...) but never internal/server; see ADR-0003 and ADR-0002.
package ui

import (
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.MouseClickMsg:
		return m.handleClick(msg.Mouse())

	case tea.KeyPressMsg:
		return m.handleKey(msg)

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
		applyMongoExportMsg(msg)
		return m, nil

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
		applyExportViewMsg(msg)
		return m, nil

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
		applyConsoleExportMsg(msg)
		return m, nil

	case redisConsoleMsg:
		applyRedisConsoleMsg(msg)
		return m, nil
	}
	return m, nil
}

// openDataView opens a new Data view tab for n (a Table or, for mongodb, a
// Collection) and focuses it, mirroring what a confirmed double-click does
// (spec: "Enter or double-click on a Table to open its Data view"). It is
// a no-op for anything that is not a Table-level node or a redis key.
func (m Model) openDataView(n *node) (Model, tea.Cmd) {
	if n == nil || (n.kind != kindTable && n.kind != kindKey) {
		return m, nil
	}
	a, ok := adapterUIs[n.adapter]
	if !ok {
		return m, nil
	}
	t, cmd := a.openData(m.pool, n)
	m.bindAccess(t, n.profile)
	m.tabs = append(m.tabs, t)
	m.activeTab = len(m.tabs) - 1
	m.focus = paneMain
	return m, cmd
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
	m.tabs = append(m.tabs, t)
	m.activeTab = len(m.tabs) - 1
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
// (spec: "d on a Table opens its DDL in a tab").
func (m *Model) openDDLIfTable(n *node) tea.Cmd {
	if n == nil || !n.isDDLTable() {
		return nil
	}
	t, cmd := openDDLCmd(m.pool, n.profile, n.adapter, n.path)
	m.tabs = append(m.tabs, t)
	m.activeTab = len(m.tabs) - 1
	return cmd
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C cancels the focused pane's running work and never quits,
	// whatever has keyboard focus (even a text field).
	if key.Matches(msg, m.keys.Cancel) {
		m.cancelFocused()
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
		m.showHelp = false
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
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
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
		expanding := key.Matches(msg, m.keys.Expand)
		var sel *node
		if expanding {
			sel = m.tree.selectedNode()
		}
		var treeCmd tea.Cmd
		m.tree, treeCmd = m.tree.Update(msg, m.keys, m.pool)
		if !expanding {
			return m, treeCmd
		}
		var dvCmd tea.Cmd
		m, dvCmd = m.openDataView(sel)
		return m, tea.Batch(treeCmd, dvCmd)
	}

	if t := m.activeTabOrNil(); t != nil {
		// The cell popup is modal, but copying the value it shows is
		// what it offers ("y copy" in its footer).
		if c, ok := t.(copier); ok && popupOpen(t) && key.Matches(msg, m.keys.Copy) {
			return m, copyCmd(c)
		}
		if !t.EditingText() {
			if c, ok := t.(copier); ok && key.Matches(msg, m.keys.Copy) {
				return m, copyCmd(c)
			}
			if c, ok := t.(rowCopier); ok && key.Matches(msg, m.keys.CopyRow) {
				text, ok := c.CopyRowText()
				if !ok {
					return m, nil
				}
				return m, tea.SetClipboard(text)
			}
		}
		return m, t.Update(msg, m.keys, m.pool)
	}
	return m, nil
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

// copyCmd copies c's current content via OSC52; it is a no-op when the
// tab has nothing to copy yet (spec: "the copy action emits the full text
// via OSC52").
func copyCmd(c copier) tea.Cmd {
	text, ok := c.CopyText()
	if !ok {
		return nil
	}
	return tea.SetClipboard(text)
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
	sel := m.tree.selectedNode()
	var treeCmd tea.Cmd
	m.tree, treeCmd = m.tree.activate(m.pool)
	var dvCmd tea.Cmd
	m, dvCmd = m.openDataView(sel)
	return m, tea.Batch(treeCmd, dvCmd)
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
		if i := tabAt(m.tabs, m.activeTab, mainX); i >= 0 {
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
		content = mutedStyle.Render("No tabs open.")
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
	if m.focus == paneTree {
		// DDL and Console go right after the navigation/expand keys so a
		// truncated hint line drops Refresh/Disconnect first.
		h := m.keys.treeHelp()
		out := append([]key.Binding{}, h[:3]...)
		if m.ddlAvailable() {
			out = append(out, m.keys.DDL)
		}
		if consoleProfile(m.tree.selectedNode()) != "" {
			out = append(out, m.keys.Console)
		}
		return append(out, h[3:]...)
	}
	if t := m.activeTabOrNil(); t != nil {
		if popupOpen(t) {
			return []key.Binding{m.keys.Copy, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close"))}
		}
		return t.Help(m.keys)
	}
	return nil
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

	hints := append(m.paneHelp(), m.keys.globalHelp()...)
	status := renderStatusBar(m.width, m.mode(), hints, m.tree.filterQuery)
	content := body + "\n" + status

	if m.showHelp {
		content = overlay(content, renderHelp(m.width, m.height, m.keys), m.width, m.height)
	}

	v.SetContent(content)
	return v
}

// mode names what has the keyboard for the status bar's badge: the help
// overlay, the tree (or its filter field), or the active tab's kind, with
// INSERT while a Console's editor or a tab's text field takes the keys.
func (m Model) mode() string {
	switch {
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
	if t.EditingText() {
		return "INSERT"
	}
	return t.Heading().tag
}
