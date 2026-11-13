package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// slowFrame is framePane's lipgloss rendering, which fastFrame must match.
func slowFrame(content string, w, h int, focused bool) string {
	inner := lipgloss.NewStyle().Width(w).Height(h).MaxWidth(w).MaxHeight(h).Render(content)
	return paneBorder(focused).Render(inner)
}

func TestFastFrameMatchesTheLipglossRendering(t *testing.T) {
	styled := accentStyle.Render("key") + " " + mutedStyle.Render("desc")
	tests := []struct {
		name    string
		content string
		w, h    int
	}{
		{"empty", "", 10, 3},
		{"short lines", "a\nbb\nccc", 10, 5},
		{"exact width", "0123456789\nx", 10, 2},
		{"taller than the frame", "1\n2\n3\n4\n5", 6, 3},
		{"trailing newline", "a\n", 6, 3},
		{"styled", styled + "\n" + styled, 20, 4},
		{"wide runes", "日本語\nкириллица", 12, 3},
		{"blank lines", "\n\nx\n", 5, 5},
	}
	for _, tt := range tests {
		for _, focused := range []bool{false, true} {
			got, ok := fastFrame(tt.content, tt.w, tt.h, focused)
			if !ok {
				t.Errorf("%s: fast path declined content that fits", tt.name)
				continue
			}
			if want := slowFrame(tt.content, tt.w, tt.h, focused); got != want {
				t.Errorf("%s (focused %v):\n got %q\nwant %q", tt.name, focused, got, want)
			}
		}
	}
}

func TestFastFrameDeclinesWhatLipglossWouldRewrap(t *testing.T) {
	for _, content := range []string{strings.Repeat("x", 11), "a\tb", "a\r\nb"} {
		if _, ok := fastFrame(content, 10, 3, false); ok {
			t.Errorf("fast path took %q", content)
		}
	}
}

func BenchmarkFramePane(b *testing.B) {
	g := benchGrid(200, 40)
	content := g.view(196, 46, true)
	b.ReportAllocs()
	for b.Loop() {
		_ = framePane(content, "", 200, 50, true)
	}
}

// BenchmarkFramePaneLipgloss is the same frame through lipgloss, the
// rendering fastFrame replaces, for comparison.
func BenchmarkFramePaneLipgloss(b *testing.B) {
	g := benchGrid(200, 40)
	content := g.view(196, 46, true)
	b.ReportAllocs()
	for b.Loop() {
		_ = slowFrame(content, 198, 48, true)
	}
}
