package ui

import (
	"context"
	"sync"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/config"
)

// connPool holds one lazily opened, long-lived connection per (Profile,
// Database) pair for the life of the program (spec: "Connections"). Only a
// postgres Profile that discovers its Databases (ADR-0004) uses more than
// one Database per Profile; every other connection is keyed with database
// "". The Catalog tree calls Open before every Catalog fetch: a cached
// connection is reused, and a missing one is opened fresh. Reopen is what
// the load commands call once after a fetch on the cached connection
// fails, so a connection the server dropped recovers on the very next
// action without the user doing anything. Drop is the explicit disconnect
// action; otherwise a connection only closes when the process exits.
type connPool struct {
	mu    sync.Mutex
	conns map[poolKey]adapters.DB
	// open is the real connect call, injected so tests never touch a
	// driver or network. database is "" for the Profile's own (default)
	// database.
	open func(profile, database string) (adapters.DB, error)
}

// poolKey identifies one cached connection.
type poolKey struct{ profile, database string }

// newConnPool builds a connPool that opens Profiles from cfg.
func newConnPool(cfg *config.Config) *connPool {
	return &connPool{
		conns: map[poolKey]adapters.DB{},
		open: func(name, database string) (adapters.DB, error) {
			table, err := cfg.Profile(name)
			if err != nil {
				return nil, err
			}
			return adapters.OpenDatabase(context.Background(), table, database)
		},
	}
}

// Open returns the cached connection for profile's default database,
// opening one if none is cached yet.
func (p *connPool) Open(profile string) (adapters.DB, error) {
	return p.OpenIn(profile, "")
}

// OpenIn is Open for one Database of profile.
func (p *connPool) OpenIn(profile, database string) (adapters.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := poolKey{profile, database}
	if c, ok := p.conns[k]; ok {
		return c, nil
	}
	c, err := p.open(profile, database)
	if err != nil {
		return nil, err
	}
	p.conns[k] = c
	return c, nil
}

// OpenDedicated opens a new connection for profile that the pool neither
// caches nor closes; the caller owns it. A SQL Console uses it so its
// pinned session is never shared with, or dropped by, the Catalog tree.
func (p *connPool) OpenDedicated(profile string) (adapters.DB, error) {
	return p.open(profile, "")
}

// OpenDedicatedIn is OpenDedicated bound to one Database of profile.
func (p *connPool) OpenDedicatedIn(profile, database string) (adapters.DB, error) {
	return p.open(profile, database)
}

// Reopen discards and closes any cached connection for profile's default
// database and opens a fresh one.
func (p *connPool) Reopen(profile string) (adapters.DB, error) {
	return p.ReopenIn(profile, "")
}

// ReopenIn is Reopen for one Database of profile; the Profile's other
// connections are untouched.
func (p *connPool) ReopenIn(profile, database string) (adapters.DB, error) {
	p.mu.Lock()
	p.dropKey(poolKey{profile, database})
	p.mu.Unlock()
	return p.OpenIn(profile, database)
}

// Drop closes and discards every cached connection of profile, whatever
// Database it is bound to.
func (p *connPool) Drop(profile string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.conns {
		if k.profile == profile {
			p.dropKey(k)
		}
	}
}

// DropIn closes and discards the cached connection for one (profile,
// database) pair, if any.
func (p *connPool) DropIn(profile, database string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dropKey(poolKey{profile, database})
}

// dropKey is the unlocked core of Drop, DropIn and Reopen.
func (p *connPool) dropKey(k poolKey) {
	if c, ok := p.conns[k]; ok {
		c.Close()
		delete(p.conns, k)
	}
}
