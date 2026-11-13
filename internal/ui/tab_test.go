package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// stubTab is a minimal tab implementation for testing the tab bar and the
// generic tab-management plumbing without any real Data view.
type stubTab struct {
	name string
}

func (s *stubTab) Title() string                                     { return s.name }
func (s *stubTab) Heading() tabHeading                               { return tabHeading{tag: "STUB", object: s.name} }
func (s *stubTab) Update(tea.KeyPressMsg, keyMap, *connPool) tea.Cmd { return nil }
func (s *stubTab) View(int, int, bool) string                        { return s.name + " content" }
func (s *stubTab) EditingText() bool                                 { return false }
func (s *stubTab) Help(keyMap) []key.Binding                         { return nil }

func TestTabAtFindsTheSegmentContainingX(t *testing.T) {
	tabs := []tab{&stubTab{name: "aa"}, &stubTab{name: "bbbbbbbb"}}
	segs := tabSegments(tabs, 0)
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	// The first segment always starts at x=0.
	if got := tabAt(tabs, 0, 80, 0); got != 0 {
		t.Errorf("tabAt(x=0) = %d, want 0", got)
	}
	// Just past the first segment's width must land in the second.
	if got := tabAt(tabs, 0, 80, segs[0].width); got != 1 {
		t.Errorf("tabAt(x=%d) = %d, want 1", segs[0].width, got)
	}
}

func TestTabAtPastTheLastSegmentIsMiss(t *testing.T) {
	tabs := []tab{&stubTab{name: "a"}}
	segs := tabSegments(tabs, 0)
	if got := tabAt(tabs, 0, 200, segs[0].width+100); got != -1 {
		t.Errorf("got %d, want -1 for a click past every tab", got)
	}
}

func TestTabAtOnNoTabsIsMiss(t *testing.T) {
	if got := tabAt(nil, 0, 80, 0); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}

func TestRenderTabBarContainsEveryTitle(t *testing.T) {
	tabs := []tab{&stubTab{name: "pg-dev: widgets"}, &stubTab{name: "ch-dev: widgets"}}
	out := renderTabBar(tabs, 1, 80)
	for _, want := range []string{"pg-dev: widgets", "ch-dev: widgets"} {
		if !contains(out, want) {
			t.Errorf("tab bar missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTabBarOnNoTabsIsEmpty(t *testing.T) {
	if got := strings.TrimSpace(ansi.Strip(renderTabBar(nil, 0, 80))); got != "" {
		t.Errorf("got %q, want a blank bar", got)
	}
}

// manyTabs builds n stub tabs named t0..t(n-1), each segment the same
// width.
func manyTabs(n int) []tab {
	tabs := make([]tab, n)
	for i := range tabs {
		tabs[i] = &stubTab{name: fmt.Sprintf("table_%02d", i)}
	}
	return tabs
}

func TestRenderTabBarOverflowShowsAWindowAroundTheActiveTab(t *testing.T) {
	tabs := manyTabs(12)
	tests := []struct {
		name      string
		active    int
		wantLeft  bool
		wantRight bool
	}{
		{"first", 0, false, true},
		{"middle", 6, true, true},
		{"last", 11, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bar := ansi.Strip(renderTabBar(tabs, tt.active, 80))
			if w := lipgloss.Width(bar); w != 80 {
				t.Errorf("bar width = %d, want 80", w)
			}
			if !strings.Contains(bar, fmt.Sprintf("table_%02d", tt.active)) {
				t.Errorf("active tab missing from %q", bar)
			}
			if !strings.HasSuffix(bar, "[ ] · x ") {
				t.Errorf("tab keys hint missing from %q", bar)
			}
			if got := strings.Contains(bar, "‹ "); got != tt.wantLeft {
				t.Errorf("left marker shown = %v, want %v: %q", got, tt.wantLeft, bar)
			}
			if got := strings.Contains(bar, " ›"); got != tt.wantRight {
				t.Errorf("right marker shown = %v, want %v: %q", got, tt.wantRight, bar)
			}
		})
	}
}

func TestTabAtMatchesTheOverflowWindow(t *testing.T) {
	tabs := manyTabs(12)
	const width, active = 80, 6
	items, _ := tabBarLayout(tabs, active, width)
	pos := 0
	for _, it := range items {
		if got := tabAt(tabs, active, width, pos); got != it.tab {
			t.Errorf("tabAt(x=%d) = %d, want %d", pos, got, it.tab)
		}
		pos += it.width
	}
	// The left marker stands for the nearest hidden tab before the window.
	if items[0].tab != items[1].tab-1 {
		t.Errorf("left marker maps to tab %d, want %d", items[0].tab, items[1].tab-1)
	}
	if last := items[len(items)-1]; last.tab != items[len(items)-2].tab+1 {
		t.Errorf("right marker maps to tab %d, want %d", last.tab, items[len(items)-2].tab+1)
	}
}

func TestRenderTabBarCutsATabTooWideForTheBar(t *testing.T) {
	tabs := []tab{&stubTab{name: strings.Repeat("x", 100)}}
	bar := renderTabBar(tabs, 0, 40)
	if w := lipgloss.Width(bar); w != 40 {
		t.Errorf("bar width = %d, want 40: %q", w, ansi.Strip(bar))
	}
}
