package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// tab is one entry of the tab area to the right of the Catalog tree. A SQL
// Data view (this ticket) and a mongodb Data view are the kinds that exist
// today; DDL and Console tabs are later tickets behind the same interface,
// so the tab bar, focus routing, and the status bar's per-pane hints never
// need to know which kind is active.
type tab interface {
	// Title names the tab in the tab bar: the owning Profile and object.
	Title() string
	// Heading is the tab bar's view of the same identity: the kind tag
	// (DATA, DDL, KEY, SQL, MONGO, REDIS), the Profile, and the object.
	Heading() tabHeading
	// Update handles one key press while this tab is active, mutating
	// the tab in place and returning a Cmd for any background work it
	// starts.
	Update(msg tea.KeyPressMsg, keys keyMap, pool *connPool) tea.Cmd
	// View renders the tab's own content, below the tab bar.
	View(width, height int, focused bool) string
	// EditingText reports whether a text field inside the tab currently
	// has keyboard focus, so the single-letter global and tab-management
	// keys stay disabled while the user is typing (matching the tree's
	// own Esc-to-leave-a-text-field rule).
	EditingText() bool
	// Help lists this tab kind's own key bindings for the status bar and
	// keymap overlay while it is the active tab.
	Help(keys keyMap) []key.Binding
}

var (
	tabBarStyle      = lipgloss.NewStyle().Background(colorBarBg)
	tabInactiveStyle = tabBarStyle.Foreground(colorSubtle)
	tabActiveStyle   = lipgloss.NewStyle().Background(colorSelBg).Foreground(colorBright).Bold(true)
	tabSepStyle      = tabBarStyle.Foreground(colorBorder)
)

// tabSegment is one rendered tab bar entry together with the display width
// renderTabBar gave it, so tabAt can hit-test exactly the layout that was
// drawn.
type tabSegment struct {
	text  string
	width int
}

// tabSegments renders every tab as " TAG profile object " followed by a
// separator, the active one highlighted with its tag in the accent color;
// renderTabBar and tabAt both build on this so they can never disagree
// about where one tab ends and the next begins.
func tabSegments(tabs []tab, active int) []tabSegment {
	segs := make([]tabSegment, len(tabs))
	for i, t := range tabs {
		h := t.Heading()
		base, tag, prof := tabInactiveStyle, tabInactiveStyle.Foreground(colorMuted).Bold(true), tabInactiveStyle.Foreground(colorMuted)
		if i == active {
			base, tag, prof = tabActiveStyle, tabActiveStyle.Foreground(colorAccent), tabActiveStyle.Foreground(colorMuted).Bold(false)
		}
		parts := base.Render(" ") + tag.Render(h.tag) + base.Render(" ")
		if h.profile != "" {
			parts += prof.Render(h.profile) + base.Render(" ")
		}
		parts += base.Render(h.object+" ") + tabSepStyle.Render("│")
		segs[i] = tabSegment{text: parts, width: lipgloss.Width(parts)}
	}
	return segs
}

// tabBarHint is the tab keys hinted at the tab bar's right end.
const tabBarHint = " [ ] · x "

// tabMarker renders the "‹ N" or "N ›" marker counting the tabs hidden on
// one side of the tab bar's window, "" when none are.
func tabMarker(n int, left bool) string {
	if n == 0 {
		return ""
	}
	text := fmt.Sprintf(" ‹ %d ", n)
	if !left {
		text = fmt.Sprintf(" %d › ", n)
	}
	return tabBarStyle.Foreground(colorAccent).Bold(true).Render(text) + tabSepStyle.Render("│")
}

// tabBarItem is one entry of the tab bar as laid out: a tab, or a hidden
// tabs marker, which a click treats as the nearest hidden tab on its side.
type tabBarItem struct {
	text  string
	width int
	tab   int
}

// tabBarLayout lays the tab bar out for width cells: a window of tabs
// around the active one, as many as fit, with a "‹ N" marker for the tabs
// hidden before it and an "N ›" marker for those after, and the tab keys
// hint at the right end whenever it fits next to the active tab. A tab
// alone wider than the room left is cut to fit. renderTabBar and tabAt
// both build on this, so a click always hits what was drawn.
func tabBarLayout(tabs []tab, active, width int) (items []tabBarItem, hint string) {
	if len(tabs) == 0 || width <= 0 {
		return nil, ""
	}
	active = clampInt(active, 0, len(tabs)-1)
	segs := tabSegments(tabs, active)
	hint = tabBarStyle.Foreground(colorMuted).Render(tabBarHint)
	room := width - lipgloss.Width(hint)
	if room < min(segs[active].width, 12) {
		hint, room = "", width
	}
	used := func(lo, hi int) int {
		w := lipgloss.Width(tabMarker(lo, true)) + lipgloss.Width(tabMarker(len(segs)-hi, false))
		for i := lo; i < hi; i++ {
			w += segs[i].width
		}
		return w
	}
	lo, hi := active, active+1
	for {
		grew := false
		if hi < len(segs) && used(lo, hi+1) <= room {
			hi++
			grew = true
		}
		if lo > 0 && used(lo-1, hi) <= room {
			lo--
			grew = true
		}
		if !grew {
			break
		}
	}
	if m := tabMarker(lo, true); m != "" {
		items = append(items, tabBarItem{m, lipgloss.Width(m), lo - 1})
	}
	for i := lo; i < hi; i++ {
		items = append(items, tabBarItem{segs[i].text, segs[i].width, i})
	}
	if m := tabMarker(len(segs)-hi, false); m != "" {
		items = append(items, tabBarItem{m, lipgloss.Width(m), hi})
	}
	if over := used(lo, hi) - room; over > 0 {
		// Only the active tab is shown and it is too wide: cut it.
		for i := range items {
			if items[i].tab == active && items[i].text == segs[active].text {
				w := max(items[i].width-over, 1)
				items[i].text = ansi.Truncate(items[i].text, w, "…")
				items[i].width = lipgloss.Width(items[i].text)
			}
		}
	}
	return items, hint
}

// renderTabBar renders a window of the open tabs around the active one,
// highlighted, on a bar exactly width cells wide with the tab keys hinted
// at its right end (see tabBarLayout).
func renderTabBar(tabs []tab, active int, width int) string {
	items, hint := tabBarLayout(tabs, active, width)
	var b strings.Builder
	w := 0
	for _, it := range items {
		b.WriteString(it.text)
		w += it.width
	}
	if gap := width - w - lipgloss.Width(hint); gap > 0 {
		b.WriteString(tabBarStyle.Render(strings.Repeat(" ", gap)))
	}
	b.WriteString(hint)
	return b.String()
}

// tabAt returns the index of the tab drawn at x on a tab bar width cells
// wide (a hidden tabs marker stands for the nearest hidden tab on its
// side), or -1 when x falls past every tab.
func tabAt(tabs []tab, active, width, x int) int {
	items, _ := tabBarLayout(tabs, active, width)
	pos := 0
	for _, it := range items {
		if x >= pos && x < pos+it.width {
			return it.tab
		}
		pos += it.width
	}
	return -1
}

// tabHeading is how the tab bar names a tab: its kind tag, its Profile,
// and its object.
type tabHeading struct {
	tag, profile, object string
}

// cellPopupHolder is implemented by a tab with a grid, so the status bar
// can show the cell popup's own mode and keys while it is open.
type cellPopupHolder interface {
	cellPopupOpen() bool
}

// popupOpen reports whether t is showing a grid's cell popup.
func popupOpen(t tab) bool {
	p, ok := t.(cellPopupHolder)
	return ok && p.cellPopupOpen()
}
