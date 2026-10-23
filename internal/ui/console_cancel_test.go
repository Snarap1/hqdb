package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCancelledConsoleStatementThatResetTheSessionSaysSo(t *testing.T) {
	tb := newConsoleTab("pg-dev", nil)
	applyConsoleMsg(consoleMsg{tab: tb, stmt: "SELECT pg_sleep(9)", cancelled: true, sessionReset: true})
	v := ansi.Strip(tb.View(120, 30, true))
	if !strings.Contains(v, sessionResetNotice) {
		t.Errorf("view must explain the session reset:\n%s", v)
	}
	if !strings.Contains(sessionResetNotice, "SET values and temp tables are lost") {
		t.Errorf("notice %q must name what is lost", sessionResetNotice)
	}
}

func TestCancelledConsoleStatementWithoutASessionOnlySaysCancelled(t *testing.T) {
	tb := newConsoleTab("pg-dev", nil)
	applyConsoleMsg(consoleMsg{tab: tb, stmt: "SELECT 1", cancelled: true})
	v := ansi.Strip(tb.View(120, 30, true))
	if !strings.Contains(v, "statement cancelled") || strings.Contains(v, "session reset") {
		t.Errorf("view must only say the statement was cancelled:\n%s", v)
	}
}

func TestCancelledConsoleExportThatResetTheSessionSaysSo(t *testing.T) {
	tb := newConsoleTab("pg-dev", nil)
	applyConsoleExportMsg(consoleExportMsg{tab: tb, cancelled: true, sessionReset: true})
	if !strings.Contains(tb.exportStatus, "export cancelled") || !strings.Contains(tb.exportStatus, sessionResetNotice) {
		t.Errorf("export status = %q, want the cancellation and the session reset", tb.exportStatus)
	}
}
