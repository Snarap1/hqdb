package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// The palette is the GitHub-dark scheme of docs/ui/TUI database client.html
// with one amber accent for focus, keys, and badges. Colors are given in
// true color; Bubble Tea downsamples them to whatever the terminal
// supports. The border, selection, and bright text are neutral grays
// rather than the mockup's blue-tinted ones, because the tinted ones
// downsample to teal and navy on a 256-color terminal. The base background
// is left to the terminal: only bars, headers, and the selection paint
// their own.
var (
	colorText    = lipgloss.Color("#c9d1d9")
	colorBright  = lipgloss.Color("#f0f3f6")
	colorWhite   = lipgloss.Color("#ffffff")
	colorMuted   = lipgloss.Color("#7d8590")
	colorSubtle  = lipgloss.Color("#9ba4ae")
	colorGutter  = lipgloss.Color("#484f58")
	colorBorder  = lipgloss.Color("#3d444d")
	colorAccent  = lipgloss.Color("#e3a548")
	colorOnBadge = lipgloss.Color("#0d1117")
	colorBarBg   = lipgloss.Color("#090c10")
	colorStatBg  = lipgloss.Color("#161b22")
	colorSelBg   = lipgloss.Color("#30363d")
	colorGreen   = lipgloss.Color("#7ee787")
	colorBlue    = lipgloss.Color("#79c0ff")
	colorLight   = lipgloss.Color("#a5d6ff")
	colorRed     = lipgloss.Color("#ff7b72")
	colorPurple  = lipgloss.Color("#d2a8ff")
)

var (
	mutedStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	brightStyle = lipgloss.NewStyle().Foreground(colorBright).Bold(true)
	accentStyle = lipgloss.NewStyle().Foreground(colorAccent)
	errorStyle  = lipgloss.NewStyle().Foreground(colorRed)
	okStyle     = lipgloss.NewStyle().Foreground(colorGreen)
	badgeStyle  = lipgloss.NewStyle().Background(colorAccent).Foreground(colorOnBadge).Bold(true).Padding(0, 1)
)

// paneBorder is the frame around the tree and the tab content: the accent
// color while the pane has focus, the quiet border color otherwise.
func paneBorder(focused bool) lipgloss.Style {
	c := colorBorder
	if focused {
		c = colorAccent
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c)
}

// framePane draws content inside a rounded border sized to exactly
// width x height cells, with title set into the top edge. Content is
// clipped to the inner (width-2) x (height-2) area.
func framePane(content, title string, width, height int, focused bool) string {
	if width < 2 || height < 2 {
		return ""
	}
	inner := lipgloss.NewStyle().Width(width - 2).Height(height - 2).MaxWidth(width - 2).MaxHeight(height - 2).Render(content)
	box := paneBorder(focused).Render(inner)
	if title == "" {
		return box
	}
	lines := strings.SplitN(box, "\n", 2)
	edge := paneBorder(focused).GetBorderTopForeground()
	label := " " + title + " "
	room := width - 4
	if room < 1 || lipgloss.Width(label) > room {
		return box
	}
	top := lipgloss.NewStyle().Foreground(edge).Render("╭─") + label +
		lipgloss.NewStyle().Foreground(edge).Render(strings.Repeat("─", width-3-lipgloss.Width(label))+"╮")
	if len(lines) == 1 {
		return top
	}
	return top + "\n" + lines[1]
}

// headerLine renders a tab's one-line header: title in bold, sub muted
// after it, and right flush to the right edge, clipped to width.
func headerLine(width int, title, sub, right string) string {
	left := brightStyle.Render(title)
	if sub != "" {
		left += "  " + mutedStyle.Render(sub)
	}
	return spread(width, left, right)
}

// spread lays left and right out on one line of width cells with the gap
// between them filled. When both do not fit, left is cut short so right
// (a status or badge) stays visible; right alone wider than width is
// dropped.
func spread(width int, left, right string) string {
	rw := lipgloss.Width(right)
	if right == "" || rw+1 > width {
		return lipgloss.NewStyle().Inline(true).MaxWidth(max(width, 0)).Render(left)
	}
	left = lipgloss.NewStyle().Inline(true).MaxWidth(width - rw - 1).Render(left)
	return left + strings.Repeat(" ", width-lipgloss.Width(left)-rw) + right
}

// keyHint renders one binding as an accent key followed by its muted
// description.
func keyHint(b key.Binding) string {
	h := b.Help()
	return accentStyle.Render(h.Key) + " " + lipgloss.NewStyle().Foreground(colorSubtle).Render(h.Desc)
}

// overlay composites box centered over base, which is width x height.
func overlay(base, box string, width, height int) string {
	x := max((width-lipgloss.Width(box))/2, 0)
	y := max((height-lipgloss.Height(box))/2, 0)
	return lipgloss.NewCanvas(width, height).Compose(lipgloss.NewLayer(base)).Compose(lipgloss.NewLayer(box).X(x).Y(y).Z(1)).Render()
}
