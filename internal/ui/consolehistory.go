package ui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/consolestate"
)

// consoleHistory is a Console's persisted state for one Profile (ticket
// 16): the editor buffer, saved on every change and restored when a
// Console for that Profile opens, and the executed-statement history,
// recalled into the editor. It knows nothing about the Adapter, so the
// SQL, Redis, and MongoDB Consoles all embed one next to their editor.
//
// Persistence is best effort: a missing or unwritable state directory
// leaves the Console fully usable, just without memory across restarts.
type consoleHistory struct {
	store   *consolestate.Store
	profile string

	// entries is the history, oldest first, loaded when the Console opens
	// and extended as statements run. pos indexes the recalled entry, or
	// equals len(entries) when the editor holds the user's own draft.
	entries []string
	pos     int
	// draft is the buffer as it was before the first recall step, given
	// back when stepping forward past the newest entry.
	draft string
}

func newConsoleHistory(store *consolestate.Store, profile string) consoleHistory {
	h := store.History(profile)
	return consoleHistory{store: store, profile: profile, entries: h, pos: len(h)}
}

// restoredBuffer is the buffer saved by an earlier Console of the Profile.
func (h *consoleHistory) restoredBuffer() string { return h.store.LoadBuffer(h.profile) }

// edited saves text as the Profile's buffer after the user changed it and
// leaves history browsing.
func (h *consoleHistory) edited(text string) {
	h.pos = len(h.entries)
	_ = h.store.SaveBuffer(h.profile, text) // best effort, see type doc
}

// record appends an executed statement to the history.
func (h *consoleHistory) record(stmt string) {
	if n := len(h.entries); n == 0 || h.entries[n-1] != stmt {
		h.entries = append(h.entries, stmt)
	}
	h.pos = len(h.entries)
	_ = h.store.AppendHistory(h.profile, stmt) // best effort, see type doc
}

// recall steps dir (-1 older, +1 newer) through the history and returns e
// holding the reached entry, or the saved draft past the newest one, with
// the cursor at its end.
func (h *consoleHistory) recall(e editor, dir int) editor {
	next := h.pos + dir
	if next < 0 || next > len(h.entries) || len(h.entries) == 0 {
		return e
	}
	if h.pos == len(h.entries) {
		h.draft = e.text()
	}
	h.pos = next
	text := h.draft
	if next < len(h.entries) {
		text = h.entries[next]
	}
	_ = h.store.SaveBuffer(h.profile, text) // best effort, see type doc
	return editor{}.insert(text)
}

// position is "history i/n" while a recalled entry is shown, else "".
func (h *consoleHistory) position() string {
	if h.pos >= len(h.entries) {
		return ""
	}
	return fmt.Sprintf("history %d/%d", h.pos+1, len(h.entries))
}

// editKey applies a key typed into a Console editor e: the history keys
// recall, anything else edits e and saves a changed buffer. Every Console
// kind routes its editor keys through it.
func (h *consoleHistory) editKey(e editor, msg tea.KeyPressMsg, keys keyMap) editor {
	switch {
	case key.Matches(msg, keys.HistoryPrev):
		return h.recall(e, -1)
	case key.Matches(msg, keys.HistoryNext):
		return h.recall(e, 1)
	}
	before := e.text()
	e = e.update(msg)
	if e.text() != before {
		h.edited(e.text())
	}
	return e
}
