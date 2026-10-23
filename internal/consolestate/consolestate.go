// Package consolestate persists a Console's per-Profile state for the hqdb
// terminal UI: the editor buffer and the history of executed statements.
// It is adapter-agnostic (a buffer is text, a history entry is a
// statement), so the SQL, Redis, and MongoDB Consoles share it.
//
// Files live flat in one directory, named after the URL path-escaped
// Profile name so no name can escape it:
//
//	<dir>/<profile>.buffer        the editor text, verbatim
//	<dir>/<profile>.history.json  a JSON array of statements, oldest first
//
// State is a convenience: reads of a missing or unreadable directory
// return empty state, and a nil *Store persists nothing.
package consolestate

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
)

// MaxHistory is how many of the newest statements a Profile's history keeps.
const MaxHistory = 500

// DefaultDir is the XDG state directory's hqdb subdirectory:
// $XDG_STATE_HOME/hqdb, else ~/.local/state/hqdb.
func DefaultDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "hqdb")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "hqdb")
}

// Store reads and writes Console state under one directory.
type Store struct {
	dir string
}

// New returns a Store rooted at dir; dir is created on the first write.
func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path(profile, suffix string) string {
	return filepath.Join(s.dir, url.PathEscape(profile)+suffix)
}

// LoadBuffer returns profile's saved editor buffer, or "" when there is none
// or it cannot be read.
func (s *Store) LoadBuffer(profile string) string {
	if s == nil {
		return ""
	}
	b, err := os.ReadFile(s.path(profile, ".buffer"))
	if err != nil {
		return ""
	}
	return string(b)
}

// SaveBuffer replaces profile's saved editor buffer.
func (s *Store) SaveBuffer(profile, text string) error {
	if s == nil {
		return nil
	}
	return s.write(s.path(profile, ".buffer"), []byte(text))
}

// History returns profile's executed statements, oldest first; nil when
// there are none or they cannot be read.
func (s *Store) History(profile string) []string {
	if s == nil {
		return nil
	}
	b, err := os.ReadFile(s.path(profile, ".history.json"))
	if err != nil {
		return nil
	}
	var h []string
	if json.Unmarshal(b, &h) != nil {
		return nil
	}
	return h
}

// AppendHistory records stmt as profile's newest executed statement,
// unless it repeats the newest one, keeping at most MaxHistory entries.
func (s *Store) AppendHistory(profile, stmt string) error {
	if s == nil {
		return nil
	}
	h := s.History(profile)
	if len(h) > 0 && h[len(h)-1] == stmt {
		return nil
	}
	h = append(h, stmt)
	if len(h) > MaxHistory {
		h = h[len(h)-MaxHistory:]
	}
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return s.write(s.path(profile, ".history.json"), b)
}

// write replaces path atomically (temp file + rename), so a crash never
// leaves a half-written file behind.
func (s *Store) write(path string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
