package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func ctrlC() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

// isQuit reports whether cmd, when run, asks the program to quit.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestCtrlCWithNothingRunningDoesNotQuit(t *testing.T) {
	for _, focus := range []pane{paneTree, paneMain} {
		m := newTestModel()
		m.focus = focus
		if focus == paneMain {
			m.tabs = []tab{newConsoleTab("alpha", "", nil)}
		}
		_, cmd := m.handleKey(ctrlC())
		if isQuit(cmd) {
			t.Errorf("focus %v: Ctrl+C quit the app with nothing running", focus)
		}
	}
}

func TestCancelSetCancelsEveryRunningContextAndReportsWhetherAnyRan(t *testing.T) {
	var s cancelSet
	if s.cancelAll() {
		t.Fatalf("cancelAll on an empty set reported running work")
	}
	_, done1 := s.start()
	ctx2, _ := s.start()
	done1()
	if !s.cancelAll() {
		t.Fatalf("cancelAll did not report the running context")
	}
	if ctx2.Err() == nil {
		t.Errorf("running context not cancelled")
	}
	if s.cancelAll() {
		t.Errorf("second cancelAll still reported running work")
	}
}
