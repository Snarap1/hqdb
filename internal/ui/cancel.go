package ui

import (
	"context"
	"errors"
	"sync"
)

// errCancelled is what cancelled Catalog work reports in place of the
// driver's own context error.
var errCancelled = errors.New("cancelled")

// cancelSet tracks the contexts of one owner's running background work (a
// tab's statements, pages, counts, and exports, or the Catalog tree's
// loads), so Ctrl+C can cancel all of it at once (spec: "Ctrl+C cancels
// the running work in the focused tab"). There is no default timeout:
// work runs until it finishes or is cancelled. It is safe for concurrent
// use, since the work finishes on Bubble Tea's Cmd goroutines.
type cancelSet struct {
	mu      sync.Mutex
	next    int
	running map[int]context.CancelFunc
}

// start returns a fresh cancellable context for one piece of work and the
// done func the work must call when it finishes (cancelled or not).
func (s *cancelSet) start() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = map[int]context.CancelFunc{}
	}
	id := s.next
	s.next++
	s.running[id] = cancel
	return ctx, func() {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
		cancel()
	}
}

// cancelAll cancels every running piece of work and reports whether there
// was any.
func (s *cancelSet) cancelAll() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	had := len(s.running) > 0
	for id, cancel := range s.running {
		cancel()
		delete(s.running, id)
	}
	return had
}

// tabCanceller is implemented by a tab whose background work Ctrl+C can
// cancel.
type tabCanceller interface {
	// Cancel cancels the tab's running work, if any.
	Cancel()
}
