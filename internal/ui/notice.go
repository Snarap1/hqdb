package ui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

// noticeTTL is how long a transient notice stays in the status bar.
const noticeTTL = 3 * time.Second

// noticeMsg asks the Model to show text as the status bar's transient
// notice; a tab returns noticeCmd for it from its Update.
type noticeMsg struct{ text string }

// noticeClearMsg clears the notice it was scheduled for, unless a newer
// one has replaced it since.
type noticeClearMsg struct{ seq int }

// noticeCmd is the tea.Cmd a tab returns to show text as a notice.
func noticeCmd(text string) tea.Cmd {
	return func() tea.Msg { return noticeMsg{text: text} }
}

// setNotice shows text in the status bar and returns the tick that
// clears it noticeTTL later.
func (m *Model) setNotice(text string) tea.Cmd {
	m.notice = text
	m.noticeSeq++
	seq := m.noticeSeq
	return tea.Tick(noticeTTL, func(time.Time) tea.Msg { return noticeClearMsg{seq: seq} })
}

// noticeIf is setNotice for a non-empty text, nil otherwise.
func (m *Model) noticeIf(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	return m.setNotice(text)
}

// copiedNotice is the notice after "y" copied text.
func copiedNotice(text string) string {
	n := len([]rune(text))
	if n == 1 {
		return "copied 1 char"
	}
	return fmt.Sprintf("copied %d chars", n)
}

// exportedNotice is the notice after an export wrote n rows (or
// Documents) to dest.
func exportedNotice(n int, unit, dest string) string {
	return fmt.Sprintf("exported %d %s -> %s", n, unit, dest)
}

// ---- spinner ------------------------------------------------------------

// spinInterval is how often the spinner advances while a tab is busy.
const spinInterval = 100 * time.Millisecond

// spinMsg is the spinner's tick: it only makes the Model redraw, since a
// busy tab's spinner and elapsed time are derived from the clock.
type spinMsg struct{}

// busyTab is implemented by a tab whose background work (a page, count,
// statement, export, or load) can be running.
type busyTab interface {
	busy() bool
}

// anyBusy reports whether any open tab has work running.
func (m Model) anyBusy() bool {
	for _, t := range m.tabs {
		if b, ok := t.(busyTab); ok && b.busy() {
			return true
		}
	}
	return false
}

// armSpinner schedules the next spinner tick while any tab is busy and
// none is scheduled yet, so an idle UI never ticks.
func (m *Model) armSpinner() tea.Cmd {
	if m.spinning || !m.anyBusy() {
		return nil
	}
	m.spinning = true
	return tea.Tick(spinInterval, func(time.Time) tea.Msg { return spinMsg{} })
}

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// busyText is label with a spinner frame and the time elapsed since
// since: "⠹ Running 3.2s"; "Running…" when since is unknown (zero).
func busyText(label string, since time.Time) string {
	if since.IsZero() {
		return label + "…"
	}
	d := time.Since(since)
	frame := spinnerFrames[int(d/spinInterval)%len(spinnerFrames)]
	return fmt.Sprintf("%c %s %.1fs", frame, label, d.Seconds())
}
