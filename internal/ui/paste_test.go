package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPasteIntoConsoleEditor(t *testing.T) {
	cases := []struct {
		name    string
		editing bool
		before  string
		paste   string
		want    string
	}{
		{"multi-line SQL keeps its lines", true, "", "SELECT 1\r\nFROM t\rWHERE x", "SELECT 1\nFROM t\nWHERE x"},
		{"inserted at the cursor", true, "ab", "X", "abX"},
		{"tabs expand like the tab key", true, "", "\tq", "  q"},
		{"ignored while the results area has focus", false, "ab", "X", "ab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newTestModel()
			tb := newConsoleTab("pg", "", nil)
			tb.editor = tb.editor.insert(c.before)
			tb.editing = c.editing
			m.tabs, m.focus = []tab{tb}, paneMain
			m.Update(tea.PasteMsg{Content: c.paste})
			if got := tb.editor.text(); got != c.want {
				t.Fatalf("editor = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPasteIntoOneLineFields(t *testing.T) {
	d := newDataViewTab("pg", "postgres", []string{"public", "t"})
	d.editing, d.fieldInput = fieldWhere, "id = "
	d.Paste("1\nOR id = 2\n")
	if d.fieldInput != "id = 1 OR id = 2" {
		t.Errorf("WHERE = %q", d.fieldInput)
	}
	d.editing, d.editingPageSize, d.pageSizeInput = fieldNone, true, ""
	d.Paste(" 2a5\n")
	if d.pageSizeInput != "25" {
		t.Errorf("page size = %q", d.pageSizeInput)
	}
	d.editingPageSize = false
	d.Paste("ignored")
	if d.fieldInput != "id = 1 OR id = 2" || d.pageSizeInput != "25" {
		t.Errorf("paste with no field open changed a field")
	}
}
