package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// Pool defaults (ADR-0006).
const (
	// poolIdleTTL is how long an idle connection is kept for the next call.
	poolIdleTTL = 60 * time.Second
	// poolMaxIdle bounds the idle connections kept across every Profile.
	poolMaxIdle = 16
)

// poolKey names what a pooled connection was opened for: the Profile, the
// Database, and a hash of the Profile table it was opened from, so any
// edit of the Profile (or a different file defining the same name) opens
// a new connection instead of reusing one with old parameters.
type poolKey struct {
	profile  string
	database string
	params   string
}

// newPoolKey keys a connection to database of the Profile name, whose
// table is profile. ok is false when the table cannot be hashed; such a
// connection is not pooled.
func newPoolKey(name, database string, profile map[string]any) (poolKey, bool) {
	raw, err := json.Marshal(profile) // map keys are sorted, so the encoding is canonical
	if err != nil {
		return poolKey{}, false
	}
	sum := sha256.Sum256(raw)
	return poolKey{profile: name, database: database, params: hex.EncodeToString(sum[:])}, true
}

// idleConn is a pooled connection waiting for its next checkout.
type idleConn struct {
	key  poolKey
	conn adapters.DB
	at   time.Time // when it was released
}

// connPool keeps opened connections across tool calls (ADR-0006). A
// connection is checked out by one call at a time; concurrent calls for
// the same key each get their own. Idle connections are closed after ttl,
// when more than max are idle (least recently used first), when their
// Profile's parameters change, and when the pool closes.
type connPool struct {
	ttl time.Duration
	max int
	now func() time.Time

	mu     sync.Mutex
	idle   []idleConn        // least recently released first
	params map[string]string // Profile name -> params hash of its newest checkout
	closed bool
}

func newConnPool(ttl time.Duration, max int, now func() time.Time) *connPool {
	return &connPool{ttl: ttl, max: max, now: now, params: map[string]string{}}
}

// checkout returns an idle connection for key, or one from open. The
// returned release must be called exactly once when the call is done:
// keep reports whether the connection may serve a later call. reused is
// true for a connection that served an earlier call.
func (p *connPool) checkout(key poolKey, open func() (adapters.DB, error)) (conn adapters.DB, reused bool, release func(keep bool), err error) {
	p.mu.Lock()
	var stale []adapters.DB
	if p.params[key.profile] != key.params {
		// The Profile changed since the last checkout: nothing opened
		// from its old parameters is reused.
		p.params[key.profile] = key.params
		stale = p.takeLocked(func(c idleConn) bool { return c.key.profile == key.profile })
	}
	stale = append(stale, p.expireLocked()...)
	for i := len(p.idle) - 1; i >= 0; i-- {
		if p.idle[i].key == key {
			conn = p.idle[i].conn
			p.idle = append(p.idle[:i], p.idle[i+1:]...)
			reused = true
			break
		}
	}
	p.mu.Unlock()
	closeAll(stale)
	if conn == nil {
		if conn, err = open(); err != nil {
			return nil, false, nil, err
		}
	}
	var once sync.Once
	return conn, reused, func(keep bool) { once.Do(func() { p.release(key, conn, keep) }) }, nil
}

// release returns conn to the pool, or closes it when it must not be kept.
func (p *connPool) release(key poolKey, conn adapters.DB, keep bool) {
	p.mu.Lock()
	if !keep || p.closed || p.params[key.profile] != key.params {
		p.mu.Unlock()
		conn.Close()
		return
	}
	p.idle = append(p.idle, idleConn{key: key, conn: conn, at: p.now()})
	var evicted []adapters.DB
	for len(p.idle) > p.max {
		evicted = append(evicted, p.idle[0].conn)
		p.idle = p.idle[1:]
	}
	p.mu.Unlock()
	closeAll(evicted)
}

// sweep closes the idle connections older than ttl.
func (p *connPool) sweep() {
	p.mu.Lock()
	expired := p.expireLocked()
	p.mu.Unlock()
	closeAll(expired)
}

// run sweeps every interval until stop is closed.
func (p *connPool) run(interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			p.sweep()
		}
	}
}

// close closes every idle connection; connections still checked out are
// closed on release.
func (p *connPool) close() {
	p.mu.Lock()
	p.closed = true
	all := p.takeLocked(func(idleConn) bool { return true })
	p.mu.Unlock()
	closeAll(all)
}

func (p *connPool) expireLocked() []adapters.DB {
	cutoff := p.now().Add(-p.ttl)
	return p.takeLocked(func(c idleConn) bool { return !c.at.After(cutoff) })
}

// takeLocked removes and returns the idle connections that match.
func (p *connPool) takeLocked(match func(idleConn) bool) []adapters.DB {
	var taken []adapters.DB
	kept := p.idle[:0]
	for _, c := range p.idle {
		if match(c) {
			taken = append(taken, c.conn)
		} else {
			kept = append(kept, c)
		}
	}
	clear(p.idle[len(kept):])
	p.idle = kept
	return taken
}

// closeAll closes conns outside the pool lock: a close can block (mongodb
// disconnects with a timeout).
func closeAll(conns []adapters.DB) {
	for _, c := range conns {
		c.Close()
	}
}

// idleCount is the number of idle connections, for tests.
func (p *connPool) idleCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}
