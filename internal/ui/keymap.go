package ui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// keyMap is the set of key bindings this ticket implements. Later tickets
// add more entries (catalog actions, tab management, Data view and Console
// keys); the help overlay and the status bar hints are both driven from
// this one definition so they can never drift from what is actually wired
// up in Update.
type keyMap struct {
	ToggleTree key.Binding
	FocusNext  key.Binding
	FocusPrev  key.Binding
	Help       key.Binding
	Quit       key.Binding
	// Cancel cancels the focused pane's running work (a tab's statement,
	// page, count, or export; the tree's loads) without quitting (spec:
	// "Ctrl+C cancels the focused tab's work").
	Cancel key.Binding
	Up     key.Binding
	Down   key.Binding
	// Expand toggles a node open or closed, fetching its Catalog level the
	// first time; it is also what a confirmed double-click runs.
	Expand key.Binding
	// TreeLeft collapses the selected node, or moves the selection to its
	// parent when it is already collapsed; TreeRight expands the selected
	// node, or moves the selection to its first child when it is already
	// expanded. They share keys with Left and Right, which only act when a
	// grid has focus.
	TreeLeft  key.Binding
	TreeRight key.Binding
	// Refresh re-fetches the selected node's children from the server, or
	// retries the connection when the selected node is a broken Profile.
	Refresh key.Binding
	// Disconnect closes the selected Profile's connection; the next
	// expand opens a new one. The spec's keymap table does not assign it
	// a key, so this ticket picks shift+d (capital D), distinct from the
	// lowercase "d" this ticket gives to opening a Table's DDL.
	Disconnect key.Binding

	// Left and Right scroll a Data view's grid horizontally and move its
	// cell cursor; Up and Down (above) move the cursor vertically too
	// when a Data view tab, not the tree, has focus.
	Left  key.Binding
	Right key.Binding

	// PrevTab, NextTab, and CloseTab are global tab-management keys
	// (spec keymap: "[" / "]" / "x").
	PrevTab  key.Binding
	NextTab  key.Binding
	CloseTab key.Binding

	// NextPage, PrevPage, and PageSize are a SQL Data view's own keys
	// (spec keymap: "n" / "p"; page size has no assigned key in the
	// spec's table, so this ticket picks "s").
	NextPage key.Binding
	PrevPage key.Binding
	PageSize key.Binding
	// Where, OrderBy, and Count are a SQL Data view's WHERE field, ORDER
	// BY field, and on-demand row count (spec keymap: "f" / "o" / "#").
	Where   key.Binding
	OrderBy key.Binding
	Count   key.Binding
	// Export asks for a destination path and exports the Data view's
	// current query, unpaged, to CSV or JSON (spec keymap: "e").
	Export key.Binding
	// ToggleView switches a mongodb Data view between Document cards and
	// the grid; the spec assigns it no key, so ticket 21 picks "v".
	ToggleView key.Binding

	// Filter opens the name filter field; see catalogTree.updateFilter for
	// what happens to keys once it has focus.
	Filter key.Binding
	// DDL opens the selected Table's DDL in a tab; it does nothing (and is
	// not hinted) when the selected node is not a Table, which also covers
	// redis and mongodb nodes.
	DDL key.Binding
	// Copy copies the active tab's content via OSC52: a DDL tab's full
	// definition text, or a grid's current cell.
	Copy key.Binding

	// CellPopup, CopyRow, Search, SearchNext, and SearchPrev are the shared
	// grid's own keys (ticket 09), so every tab kind with a grid gets them.
	// The spec gives "Enter" / double-click to the popup and "/" to search;
	// it assigns no key to row copy or to moving between matches, so this
	// ticket picks "Y" and ctrl+n / ctrl+p ("n" / "p" already page).
	CellPopup  key.Binding
	CopyRow    key.Binding
	Search     key.Binding
	SearchNext key.Binding
	SearchPrev key.Binding

	// Console opens a SQL Console on the selected node's Profile (spec
	// keymap: "c"). RunStatement runs the statement under the Console
	// editor's cursor (spec: "Alt+Enter"). EditBuffer moves focus from the
	// Console's results back into its editor; the spec assigns it no key,
	// so this ticket picks "i" (Esc leaves the editor).
	Console      key.Binding
	RunStatement key.Binding
	EditBuffer   key.Binding
	// HistoryPrev and HistoryNext recall the Profile's earlier executed
	// statements into the Console editor (ticket 16); the spec assigns no
	// key, so ctrl+up / ctrl+down, which plain editing does not use.
	HistoryPrev key.Binding
	HistoryNext key.Binding
	// NewLineBelow, WordLeft, and WordRight are hints for editor keys that
	// editor.update handles itself (it also takes ctrl+backspace/delete,
	// ctrl+home/end, ctrl+u, and ctrl+k).
	NewLineBelow key.Binding
	WordLeft     key.Binding
	WordRight    key.Binding
	MoveLineUp   key.Binding
	MoveLineDown key.Binding
	DupLine      key.Binding

	// PageUp, PageDown, Top, and Bottom move the tree's selection, a
	// grid's cell cursor, a card view's cursor, a DDL tab's text, and the
	// help overlay by a screenful or to either end.
	PageUp   key.Binding
	PageDown key.Binding
	Top      key.Binding
	Bottom   key.Binding
	// JumpTab makes tab N (alt+1 to alt+9) the active one.
	JumpTab key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		ToggleTree: key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "hide/show tree")),
		FocusNext:  key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "focus")),
		FocusPrev:  key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "focus back")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "keys")),
		Quit:       key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		Cancel:     key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "cancel running work")),
		Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "select up")),
		Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "select down")),
		Expand:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "expand/collapse")),
		TreeLeft:   key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "collapse/parent")),
		TreeRight:  key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "expand/child")),
		Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh/reconnect")),
		Disconnect: key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "disconnect profile")),

		Left:  key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "scroll left")),
		Right: key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "scroll right")),

		PrevTab:  key.NewBinding(key.WithKeys("["), key.WithHelp("[", "previous tab")),
		NextTab:  key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next tab")),
		CloseTab: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "close tab")),

		NextPage:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next page")),
		PrevPage:   key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "prev page")),
		PageSize:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "page size")),
		Where:      key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "where")),
		OrderBy:    key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "order by")),
		Count:      key.NewBinding(key.WithKeys("#"), key.WithHelp("#", "count rows")),
		Export:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "export")),
		ToggleView: key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "cards/grid")),
		Filter:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter by name")),
		DDL:        key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "ddl")),
		Copy:       key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy")),

		CellPopup:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "cell popup")),
		CopyRow:    key.NewBinding(key.WithKeys("Y"), key.WithHelp("Y", "copy row (JSON)")),
		Search:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search page")),
		SearchNext: key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "next match")),
		SearchPrev: key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "prev match")),

		Console:      key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "console")),
		RunStatement: key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "run statement")),
		EditBuffer:   key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "edit")),
		HistoryPrev:  key.NewBinding(key.WithKeys("ctrl+up"), key.WithHelp("ctrl+up", "older statement")),
		HistoryNext:  key.NewBinding(key.WithKeys("ctrl+down"), key.WithHelp("ctrl+down", "newer statement")),
		NewLineBelow: key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "new line below")),
		WordLeft:     key.NewBinding(key.WithKeys("ctrl+left"), key.WithHelp("ctrl+←", "word left")),
		WordRight:    key.NewBinding(key.WithKeys("ctrl+right"), key.WithHelp("ctrl+→", "word right")),
		MoveLineUp:   key.NewBinding(key.WithKeys("alt+up"), key.WithHelp("alt+↑", "move line up")),
		MoveLineDown: key.NewBinding(key.WithKeys("alt+down"), key.WithHelp("alt+↓", "move line down")),
		DupLine:      key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "duplicate line")),

		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		Top:      key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g/home", "first row")),
		Bottom:   key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G/end", "last row")),
		JumpTab: key.NewBinding(key.WithKeys("alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9"),
			key.WithHelp("alt+1..9", "go to tab N")),
	}
}

// jumpTabIndex is the zero-based tab index msg jumps to (alt+1 is 0).
func (k keyMap) jumpTabIndex(msg tea.KeyPressMsg) (int, bool) {
	if !key.Matches(msg, k.JumpTab) {
		return 0, false
	}
	s := msg.String()
	return int(s[len(s)-1] - '1'), true
}

// moveBy is the row delta a paging key moves a list whose screen shows
// page rows and whose cursor is at cur of total, and whether k was one.
func (k keyMap) moveBy(msg tea.KeyPressMsg, cur, total, page int) (int, bool) {
	page = max(page, 1)
	switch {
	case key.Matches(msg, k.PageUp):
		return -page, true
	case key.Matches(msg, k.PageDown):
		return page, true
	case key.Matches(msg, k.Top):
		return -cur, true
	case key.Matches(msg, k.Bottom):
		return total - 1 - cur, true
	}
	return 0, false
}

// pageHint is the paging keys as one status bar hint.
func (k keyMap) pageHint() key.Binding {
	return hint("pgup/pgdn g/G", "page/ends")
}

// pagingHint is NextPage and PrevPage as one status bar hint, so the
// paging keys take one slot of the bar's limited width.
func (k keyMap) pagingHint() key.Binding {
	return key.NewBinding(key.WithKeys(append(k.NextPage.Keys(), k.PrevPage.Keys()...)...),
		key.WithHelp(k.NextPage.Help().Key+"/"+k.PrevPage.Help().Key, "page"))
}

// globalHelp is the global tail of the status bar, reserved at its right
// end; the full keymap, grouped by area, lives in the help overlay
// (help.go).
func (k keyMap) globalHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

// treeHelp lists the tree pane's own status bar hints for the selected
// node n: Enter opens a Table's Data view and expands anything else. The
// arrow keys are self-evident and left out.
func (k keyMap) treeHelp(n *node) []key.Binding {
	enter := hint("enter", "expand/collapse")
	right := hint("→/l", "expand/child")
	if opensData(n) {
		enter = hint("enter", "open data")
		if n.kind == kindTable {
			right = hint("→/l", "columns")
		}
	}
	return []key.Binding{enter, right, k.TreeLeft, k.Filter, k.Refresh, k.Disconnect, k.pageHint()}
}

// dataViewHelp lists a SQL Data view tab's own bindings, shown in the
// status bar while it is the active tab. The arrow keys are self-evident
// and left out; a status bar too narrow for every hint drops from the end.
func (k keyMap) dataViewHelp() []key.Binding {
	return []key.Binding{k.pagingHint(), k.Count, k.Where, k.OrderBy, k.Search, k.CellPopup, k.Copy, k.CopyRow, k.Export, k.SearchNext, k.SearchPrev, k.PageSize, k.pageHint()}
}

// mongoDataViewHelp lists a mongodb Data view tab's own bindings: the
// SQL Data view's "f" / "o" relabeled as the Filter and Sort fields, the
// cards/grid toggle, and the grid's cell keys while in grid view.
func (k keyMap) mongoDataViewHelp(gridMode bool) []key.Binding {
	relabel := func(b key.Binding, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(b.Keys()...), key.WithHelp(b.Help().Key, desc))
	}
	var lead []key.Binding
	if gridMode {
		lead = k.gridHelp()
	} else {
		lead = []key.Binding{relabel(k.Expand, "expand/collapse"), k.Copy, relabel(k.CopyRow, "copy document (JSON)")}
	}
	return append(lead, k.pagingHint(), relabel(k.Where, "filter"), relabel(k.OrderBy, "sort"), k.ToggleView, k.Count, k.Export, k.PageSize)
}

// gridHelp lists the shared grid's cell keys, leading the hints of every
// tab kind that embeds a grid.
func (k keyMap) gridHelp() []key.Binding {
	return []key.Binding{k.CellPopup, k.Copy, k.CopyRow, k.Search, k.SearchNext, k.SearchPrev, k.pageHint()}
}
