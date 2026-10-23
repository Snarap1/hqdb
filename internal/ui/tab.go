package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// renderTabBar renders every open tab, the active one highlighted, on a
// bar exactly width cells wide with the tab keys hinted at its right end.
func renderTabBar(tabs []tab, active int, width int) string {
	var b strings.Builder
	for _, s := range tabSegments(tabs, active) {
		b.WriteString(s.text)
	}
	left := b.String()
	right := tabBarStyle.Foreground(colorMuted).Render(" [ ] · x ")
	if len(tabs) == 0 {
		right = ""
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		return lipgloss.NewStyle().Inline(true).MaxWidth(max(width, 0)).Render(left)
	}
	return left + tabBarStyle.Render(strings.Repeat(" ", gap)) + right
}

// tabAt returns the index of the tab whose rendered segment contains x (as
// renderTabBar laid them out, back to back, each with its own separator), or -1 when
// x falls past every tab.
func tabAt(tabs []tab, active int, x int) int {
	pos := 0
	for i, s := range tabSegments(tabs, active) {
		if x >= pos && x < pos+s.width {
			return i
		}
		pos += s.width
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
