package ui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
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
	if got := tabAt(tabs, 0, 0); got != 0 {
		t.Errorf("tabAt(x=0) = %d, want 0", got)
	}
	// Just past the first segment's width must land in the second.
	if got := tabAt(tabs, 0, segs[0].width); got != 1 {
		t.Errorf("tabAt(x=%d) = %d, want 1", segs[0].width, got)
	}
}

func TestTabAtPastTheLastSegmentIsMiss(t *testing.T) {
	tabs := []tab{&stubTab{name: "a"}}
	segs := tabSegments(tabs, 0)
	if got := tabAt(tabs, 0, segs[0].width+100); got != -1 {
		t.Errorf("got %d, want -1 for a click past every tab", got)
	}
}

func TestTabAtOnNoTabsIsMiss(t *testing.T) {
	if got := tabAt(nil, 0, 0); got != -1 {
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
