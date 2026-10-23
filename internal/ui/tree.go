package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Snarap1/hqdb/internal/config"
)

// catalogTree is the left-hand pane: one root node per Profile of the
// merged config, expanding lazily into each Profile's Catalog (Database,
// Schema, Table, Column). It never opens a connection on its own; expanding
// a node is what triggers the Profile's lazy connect.
type catalogTree struct {
	roots    []*node
	selected int

	// filtering is true while the name filter field has keyboard focus:
	// every key but Up, Down, and Esc goes into it instead of acting on
	// the tree, so single letters that would otherwise refresh or
	// disconnect a node can be typed. filterQuery is the field's current
	// text and keeps narrowing the tree after Esc leaves the field; only
	// clearing it back to "" shows the full tree again.
	filtering   bool
	filterQuery string
	filterInput textinput.Model

	// work tracks the tree's running loads and searches, which Ctrl+C
	// cancels while the tree has focus.
	work *cancelSet
}

func newCatalogTree(cfg *config.Config) catalogTree {
	ti := textinput.New()
	ti.Prompt = "" // the tree's own "/" prefix (see View) is the only prompt shown
	ti.Placeholder = "filter by name"
	return catalogTree{roots: profileNodes(cfg), filterInput: ti, work: &cancelSet{}}
}

// visible flattens the tree into the rows currently on screen: every root,
// depth-first, descending into a node's children only while it is
// expanded. While a filter query is active, a node is included only when
// its own name matches (see matches); "load more" rows are never shown,
// since the filter's server-side search (see searchCmds) makes paging
// through them to find a name pointless. Descent into an expanded node's
// children always happens regardless of whether the node itself matched,
// so a deep match is never hidden just because an ancestor's name does not
// contain the query.
func (t catalogTree) visible() []*node {
	var out []*node
	var walk func(n *node)
	walk = func(n *node) {
		if t.matches(n) {
			out = append(out, n)
		}
		if n.expanded {
			for _, c := range t.walkChildren(n) {
				walk(c)
			}
		}
	}
	for _, r := range t.roots {
		walk(r)
	}
	return out
}

// matches reports whether n passes the current filter: every node when no
// filter is active, otherwise every node but a "load more" sentinel whose
// name contains filterQuery, case-insensitively.
func (t catalogTree) matches(n *node) bool {
	if t.filterQuery == "" {
		return true
	}
	if n.kind == kindLoadMore {
		return false
	}
	return strings.Contains(strings.ToLower(n.name), strings.ToLower(t.filterQuery))
}

// walkChildren is the children visible() descends into for n: its loaded
// children, plus any server-side search hits found for the active filter
// on a level the local chunk could not see past (dropped once the filter
// clears, since they are re-derived from n.children, not merged into it).
func (t catalogTree) walkChildren(n *node) []*node {
	if t.filterQuery == "" || len(n.searchHits) == 0 {
		return n.children
	}
	return append(append([]*node{}, n.children...), n.searchHits...)
}

// clampedSelected is t.selected clamped to a valid index of rows, or -1
// when rows is empty.
func clampedSelected(selected int, rows []*node) int {
	if len(rows) == 0 {
		return -1
	}
	if selected < 0 {
		return 0
	}
	if selected >= len(rows) {
		return len(rows) - 1
	}
	return selected
}

// Selected returns the currently selected node, or nil if the tree is
// empty.
func (t catalogTree) selectedNode() *node {
	rows := t.visible()
	i := clampedSelected(t.selected, rows)
	if i < 0 {
		return nil
	}
	return rows[i]
}

// scrollTopFor returns the first visible row index that keeps selected
// inside a window of height rows, roughly centered, clamped so the window
// never runs past either end of a total-row list.
func scrollTopFor(selected, total, height int) int {
	if height <= 0 || total <= height {
		return 0
	}
	top := selected - height/2
	if top < 0 {
		top = 0
	}
	if top > total-height {
		top = total - height
	}
	return top
}

// Update handles navigation and node actions. Enter, "r", and "D" (see
// keymap.go) can return a tea.Cmd that loads a Catalog level in the
// background; every other key only moves the selection.
func (t catalogTree) Update(msg tea.Msg, keys keyMap, pool *connPool) (catalogTree, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return t, nil
	}

	if t.filtering {
		return t.updateFilter(k, pool)
	}

	rows := t.visible()
	t.selected = clampedSelected(t.selected, rows)

	switch {
	case key.Matches(k, keys.Up):
		if t.selected > 0 {
			t.selected--
		}
	case key.Matches(k, keys.Down):
		if t.selected < len(rows)-1 {
			t.selected++
		}
	case key.Matches(k, keys.Filter):
		t.filtering = true
		return t, t.filterInput.Focus()
	case key.Matches(k, keys.Expand):
		return t.activate(pool)
	case key.Matches(k, keys.Refresh):
		return t.refresh(pool)
	case key.Matches(k, keys.Disconnect):
		t.disconnect(pool)
	}
	return t, nil
}

// updateFilter handles every key while the name filter field has focus.
// Up and Down still move the selection within the narrowed list; Esc
// leaves the field without discarding filterQuery, so the tree stays
// narrowed until the query is cleared; every other key - including single
// letters like "r" or "D" that would otherwise act on the selected node -
// goes into the field itself, and a changed query re-narrows the tree and
// fires a server-side search for any visible level the local chunk cut
// short.
func (t catalogTree) updateFilter(k tea.KeyPressMsg, pool *connPool) (catalogTree, tea.Cmd) {
	switch k.String() {
	case "esc":
		t.filtering = false
		t.filterInput.Blur()
		return t, nil
	case "up":
		rows := t.visible()
		t.selected = clampedSelected(t.selected, rows)
		if t.selected > 0 {
			t.selected--
		}
		return t, nil
	case "down":
		rows := t.visible()
		t.selected = clampedSelected(t.selected, rows)
		if t.selected < len(rows)-1 {
			t.selected++
		}
		return t, nil
	}

	var cmd tea.Cmd
	t.filterInput, cmd = t.filterInput.Update(k)
	query := t.filterInput.Value()
	if query == t.filterQuery {
		return t, cmd
	}
	t.filterQuery = query
	t.selected = clampedSelected(t.selected, t.visible())
	return t, tea.Batch(cmd, t.searchCmds(pool))
}

// searchCmds fires a server-side name search (see searchCmd) for every
// currently expanded node whose children were cut short by the chunk size
// (a trailing "load more" row), which the local filter's substring match
// cannot see past. It returns nil when no filter is active or no level
// needs it.
func (t catalogTree) searchCmds(pool *connPool) tea.Cmd {
	if t.filterQuery == "" {
		return nil
	}
	var cmds []tea.Cmd
	var walk func(n *node)
	walk = func(n *node) {
		if !n.expanded {
			return
		}
		if hasLoadMore(n.children) {
			cmds = append(cmds, searchCmd(t.work, pool, n, t.filterQuery))
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, r := range t.roots {
		walk(r)
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// activate expands, collapses, or (on a load-more row) extends the
// selected node, mirroring what a confirmed double-click does.
func (t catalogTree) activate(pool *connPool) (catalogTree, tea.Cmd) {
	n := t.selectedNode()
	if n == nil || n.loading {
		return t, nil
	}
	switch n.kind {
	case kindColumn, kindKey:
		return t, nil
	case kindLoadMore:
		parent := n.parent
		parent.loading = true
		return t, loadCmd(t.work, pool, parent, true)
	default:
		if n.expanded {
			n.expanded = false
			return t, nil
		}
		if n.loaded {
			n.expanded = true
			return t, nil
		}
		n.loading = true
		return t, loadCmd(t.work, pool, n, false)
	}
}

// refresh re-fetches the selected node's level from scratch. On a Profile
// node it also forces a fresh connection, which is how a broken Profile's
// "r" retries.
func (t catalogTree) refresh(pool *connPool) (catalogTree, tea.Cmd) {
	n := t.selectedNode()
	if n == nil || n.loading || !n.kind.expandable() {
		return t, nil
	}
	if n.kind == kindProfile {
		pool.Drop(n.name)
	}
	n.loaded = false
	n.children = nil
	n.loadedN = 0
	n.loading = true
	n.expanded = true
	return t, loadCmd(t.work, pool, n, false)
}

// disconnect closes the selected Profile's connection and collapses it, so
// the next expand opens a new one. It only acts when a Profile node itself
// is selected.
func (t catalogTree) disconnect(pool *connPool) {
	n := t.selectedNode()
	if n == nil || n.kind != kindProfile {
		return
	}
	pool.Drop(n.name)
	n.expanded = false
	n.loaded = false
	n.children = nil
	n.loadedN = 0
	n.connectErr = nil
}

// selectRow moves the selection to the row at visible index i, clamped to
// range; used by mouse clicks inside the tree pane.
func (t catalogTree) selectRow(i int) catalogTree {
	t.selected = clampedSelected(i, t.visible())
	return t
}

// View renders the tree clipped to width x height, scrolled to keep the
// selection visible. Each row is the selection bar column, an expand glyph
// (or the broken marker, which sits before the name so a long name can
// never truncate it away), the name, and its muted detail; the selected
// row is also highlighted across the full width. While the filter field has focus, a "/ <query>" line is
// rendered above the rows, taking one row out of height.
func (t catalogTree) View(width, height int, focused bool) string {
	rowHeight := height
	var filterLine string
	if t.filtering {
		filterLine = lipgloss.NewStyle().Inline(true).MaxWidth(width).Render(accentStyle.Bold(true).Render("/ ") + t.filterInput.View())
		rowHeight--
		if rowHeight < 0 {
			rowHeight = 0
		}
	}

	rows := t.visible()
	var body string
	if len(rows) == 0 {
		msg := "No profiles configured."
		if t.filterQuery != "" {
			msg = "No matches."
		}
		body = mutedStyle.MaxWidth(width).Render(msg)
	} else {
		selected := clampedSelected(t.selected, rows)
		top := scrollTopFor(selected, len(rows), rowHeight)
		bottom := len(rows)
		if rowHeight > 0 && top+rowHeight < bottom {
			bottom = top + rowHeight
		}

		lines := make([]string, 0, bottom-top)
		for i := top; i < bottom; i++ {
			lines = append(lines, treeRow(rows[i], width, i == selected, focused))
		}
		body = strings.Join(lines, "\n")
	}

	if filterLine == "" {
		return body
	}
	return filterLine + "\n" + body
}

// treeRow renders one tree node as a single line exactly width cells wide.
func treeRow(n *node, width int, selected, focused bool) string {
	bg := lipgloss.NewStyle()
	if selected {
		bg = bg.Background(colorSelBg)
	}
	broken := n.fetchErr != nil || (n.kind == kindProfile && n.broken())

	glyph, glyphColor := " ", colorMuted
	switch {
	case broken:
		glyph, glyphColor = "✕", colorRed
	case n.loading:
		glyph = "…"
	case n.kind == kindLoadMore:
		glyph, glyphColor = "+", colorBlue
	case n.kind.expandable() && n.expanded:
		glyph = "▾"
	case n.kind.expandable():
		glyph = "▸"
	}

	name, meta := n.label()
	nameStyle := bg.Foreground(colorText)
	switch {
	case broken:
		nameStyle = bg.Foreground(colorRed).Bold(n.kind == kindProfile)
	case n.kind == kindLoadMore:
		nameStyle = bg.Foreground(colorBlue)
	case n.kind == kindProfile:
		nameStyle = bg.Foreground(colorBright).Bold(true)
	}
	if selected && focused {
		nameStyle = nameStyle.Foreground(colorWhite).Bold(true)
	}
	switch {
	case n.kind == kindProfile && n.validateErr != nil:
		meta = "invalid"
	case broken:
		meta = "error · r retry"
	case n.loading:
		meta = "loading…"
	}

	// The selection bar keeps the selected row findable where the
	// background is lost (a monochrome terminal, NO_COLOR).
	bar := bg.Render(" ")
	if selected {
		barStyle := bg.Foreground(colorMuted)
		if focused {
			barStyle = bg.Foreground(colorAccent)
		}
		bar = barStyle.Render("▌")
	}
	line := bar + bg.Render(strings.Repeat("  ", n.depth())) + bg.Foreground(glyphColor).Render(glyph) + bg.Render(" ") + nameStyle.Render(name)
	if meta != "" {
		line += bg.Render("  ") + bg.Foreground(colorMuted).Render(meta)
	}
	line = lipgloss.NewStyle().Inline(true).MaxWidth(width).Render(line)
	if gap := width - lipgloss.Width(line); gap > 0 {
		line += bg.Render(strings.Repeat(" ", gap))
	}
	return line
}
