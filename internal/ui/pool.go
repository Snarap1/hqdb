package ui

import (
	"sync"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/config"
)

// connPool holds one lazily opened, long-lived connection per Profile for
// the life of the program (spec: "Connections"). The Catalog tree calls
// Open before every Catalog fetch: a cached connection is reused, and a
// missing one is opened fresh. Reopen is what the load commands call once
// after a fetch on the cached connection fails, so a connection the server
// dropped recovers on the very next action without the user doing
// anything. Drop is the explicit disconnect action; otherwise a connection
// only closes when the process exits.
type connPool struct {
	mu    sync.Mutex
	conns map[string]adapters.DB
	// open is the real connect call, injected so tests never touch a
	// driver or network.
	open func(profile string) (adapters.DB, error)
}

// newConnPool builds a connPool that opens Profiles from cfg.
func newConnPool(cfg *config.Config) *connPool {
	return &connPool{
		conns: map[string]adapters.DB{},
		open: func(name string) (adapters.DB, error) {
			table, err := cfg.Profile(name)
			if err != nil {
				return nil, err
			}
			return adapters.Open(table)
		},
	}
}

// Open returns the cached connection for profile, opening one if none is
// cached yet.
func (p *connPool) Open(profile string) (adapters.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.conns[profile]; ok {
		return c, nil
	}
	c, err := p.open(profile)
	if err != nil {
		return nil, err
	}
	p.conns[profile] = c
	return c, nil
}

// OpenDedicated opens a new connection for profile that the pool neither
// caches nor closes; the caller owns it. A SQL Console uses it so its
// pinned session is never shared with, or dropped by, the Catalog tree.
func (p *connPool) OpenDedicated(profile string) (adapters.DB, error) {
	return p.open(profile)
}

// Reopen discards and closes any cached connection for profile and opens a
// fresh one.
func (p *connPool) Reopen(profile string) (adapters.DB, error) {
	p.mu.Lock()
	p.drop(profile)
	p.mu.Unlock()
	return p.Open(profile)
}

// Drop closes and discards the cached connection for profile, if any.
func (p *connPool) Drop(profile string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drop(profile)
}

// drop is Drop without the lock, for callers that already hold it or, like
// Reopen, want to call straight into Open afterwards.
func (p *connPool) drop(profile string) {
	if c, ok := p.conns[profile]; ok {
		c.Close()
		delete(p.conns, profile)
	}
}
