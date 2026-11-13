package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// fakeConn is a pooled connection that counts its closes.
type fakeConn struct {
	id     int
	closes atomic.Int32
}

func (c *fakeConn) Kind() string { return "fake" }
func (c *fakeConn) Close()       { c.closes.Add(1) }

// fakeOpener hands out numbered fakeConns and remembers them.
type fakeOpener struct {
	mu     sync.Mutex
	opened []*fakeConn
	fail   error
}

func (f *fakeOpener) open() (adapters.DB, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	c := &fakeConn{id: len(f.opened) + 1}
	f.opened = append(f.opened, c)
	return c, nil
}

func (f *fakeOpener) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.opened)
}

// fakeClock is a settable now for the pool.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func key(t *testing.T, name, database string, profile map[string]any) poolKey {
	t.Helper()
	k, ok := newPoolKey(name, database, profile)
	if !ok {
		t.Fatalf("profile %v cannot be keyed", profile)
	}
	return k
}

var pgProfile = map[string]any{"adapter": "postgres", "host": "127.0.0.1", "password": "a"}

func TestPoolKey(t *testing.T) {
	base := key(t, "pg", "", pgProfile)
	cases := []struct {
		name     string
		profile  string
		database string
		table    map[string]any
		same     bool
	}{
		{"same Profile, Database, and table", "pg", "", map[string]any{"password": "a", "host": "127.0.0.1", "adapter": "postgres"}, true},
		{"another Database", "pg", "other", pgProfile, false},
		{"another Profile name", "pg2", "", pgProfile, false},
		{"a changed password", "pg", "", map[string]any{"adapter": "postgres", "host": "127.0.0.1", "password": "b"}, false},
		{"an added key", "pg", "", map[string]any{"adapter": "postgres", "host": "127.0.0.1", "password": "a", "port": int64(5433)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := key(t, c.profile, c.database, c.table)
			if (got == base) != c.same {
				t.Errorf("key %+v vs %+v: same = %v, want %v", got, base, got == base, c.same)
			}
		})
	}
	if base.params == "" || len(base.params) != 64 {
		t.Errorf("params = %q, want a sha256 hex digest", base.params)
	}
}

func TestPoolReusesAfterRelease(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	p := newConnPool(time.Minute, 4, clock.now)
	f := &fakeOpener{}
	k := key(t, "pg", "", pgProfile)

	c1, reused, release, err := p.checkout(k, f.open)
	if err != nil || reused {
		t.Fatalf("first checkout: reused=%v err=%v", reused, err)
	}
	release(true)
	c2, reused, release2, err := p.checkout(k, f.open)
	if err != nil || !reused || c2 != c1 {
		t.Fatalf("second checkout: got %v reused=%v err=%v, want the first connection reused", c2, reused, err)
	}
	release2(true)
	if f.count() != 1 {
		t.Errorf("opened %d connections, want 1", f.count())
	}
}

func TestPoolRelease(t *testing.T) {
	cases := []struct {
		name       string
		keep       bool
		wantIdle   int
		wantClosed int32
	}{
		{"kept connection waits for the next call", true, 1, 0},
		{"connection that must not be kept is closed", false, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newConnPool(time.Minute, 4, time.Now)
			f := &fakeOpener{}
			conn, _, release, err := p.checkout(key(t, "pg", "", pgProfile), f.open)
			if err != nil {
				t.Fatal(err)
			}
			release(c.keep)
			release(c.keep) // a second release is a no-op
			if got := p.idleCount(); got != c.wantIdle {
				t.Errorf("idle = %d, want %d", got, c.wantIdle)
			}
			if got := conn.(*fakeConn).closes.Load(); got != c.wantClosed {
				t.Errorf("closes = %d, want %d", got, c.wantClosed)
			}
		})
	}
}

func TestPoolOpenError(t *testing.T) {
	p := newConnPool(time.Minute, 4, time.Now)
	want := errors.New("connection refused")
	_, _, _, err := p.checkout(key(t, "pg", "", pgProfile), (&fakeOpener{fail: want}).open)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestPoolEvictsOnProfileChange(t *testing.T) {
	p := newConnPool(time.Minute, 8, time.Now)
	f := &fakeOpener{}
	oldKey := key(t, "pg", "", pgProfile)
	oldOtherDB := key(t, "pg", "other", pgProfile)
	changed := map[string]any{"adapter": "postgres", "host": "127.0.0.1", "password": "b"}
	newKey := key(t, "pg", "", changed)
	unrelated := key(t, "my", "", map[string]any{"adapter": "mysql"})

	busyOld, _, releaseBusy, _ := p.checkout(oldKey, f.open) // checked out across the change
	idleOld, _, rel, _ := p.checkout(oldKey, f.open)
	if busyOld == idleOld {
		t.Fatal("a checked-out connection was handed out twice")
	}
	rel(true)
	idleOther, _, rel, _ := p.checkout(oldOtherDB, f.open)
	rel(true)
	idleUnrelated, _, rel, _ := p.checkout(unrelated, f.open)
	rel(true)

	fresh, reused, rel, err := p.checkout(newKey, f.open)
	if err != nil || reused || fresh == idleOld {
		t.Fatalf("checkout after the change: reused=%v err=%v, want a new connection", reused, err)
	}
	rel(true)
	for name, c := range map[string]adapters.DB{"old idle": idleOld, "old idle, other Database": idleOther} {
		if c.(*fakeConn).closes.Load() != 1 {
			t.Errorf("%s connection not closed on the Profile change", name)
		}
	}
	if idleUnrelated.(*fakeConn).closes.Load() != 0 {
		t.Error("another Profile's connection was closed")
	}
	releaseBusy(true)
	if busyOld.(*fakeConn).closes.Load() != 1 {
		t.Error("a connection from the old parameters was kept on release")
	}
	again, reused, rel, _ := p.checkout(newKey, f.open)
	if !reused || again != fresh {
		t.Errorf("the new parameters' connection was not reused")
	}
	rel(true)
}

func TestPoolIdleTTL(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	p := newConnPool(time.Minute, 4, clock.now)
	f := &fakeOpener{}
	k := key(t, "pg", "", pgProfile)
	c1, _, rel, _ := p.checkout(k, f.open)
	rel(true)

	clock.advance(59 * time.Second)
	p.sweep()
	if p.idleCount() != 1 {
		t.Fatal("connection closed before its idle TTL")
	}
	c, reused, rel, _ := p.checkout(k, f.open)
	if !reused || c != c1 {
		t.Fatal("connection within its TTL not reused")
	}
	rel(true) // the TTL restarts on release

	clock.advance(60 * time.Second)
	p.sweep()
	if p.idleCount() != 0 || c1.(*fakeConn).closes.Load() != 1 {
		t.Fatalf("sweep after the TTL: idle=%d closes=%d, want 0 and 1", p.idleCount(), c1.(*fakeConn).closes.Load())
	}

	// An expired connection is also dropped by the next checkout, without
	// waiting for a sweep.
	c2, _, rel, _ := p.checkout(k, f.open)
	rel(true)
	clock.advance(2 * time.Minute)
	c3, reused, rel, _ := p.checkout(k, f.open)
	rel(true)
	if reused || c3 == c2 || c2.(*fakeConn).closes.Load() != 1 {
		t.Errorf("checkout after the TTL reused an expired connection")
	}
}

func TestPoolMaxIdle(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	p := newConnPool(time.Hour, 2, clock.now)
	f := &fakeOpener{}
	var conns []adapters.DB
	var releases []func(bool)
	for _, db := range []string{"a", "b", "c"} {
		c, _, rel, _ := p.checkout(key(t, "pg", db, pgProfile), f.open)
		conns = append(conns, c)
		releases = append(releases, rel)
	}
	for _, rel := range releases {
		clock.advance(time.Second)
		rel(true)
	}
	if p.idleCount() != 2 {
		t.Fatalf("idle = %d, want the bound 2", p.idleCount())
	}
	if conns[0].(*fakeConn).closes.Load() != 1 {
		t.Error("the least recently released connection was not the one evicted")
	}
	for _, c := range conns[1:] {
		if c.(*fakeConn).closes.Load() != 0 {
			t.Errorf("connection %d evicted, want only the oldest", c.(*fakeConn).id)
		}
	}
}

func TestPoolClose(t *testing.T) {
	p := newConnPool(time.Minute, 4, time.Now)
	f := &fakeOpener{}
	k := key(t, "pg", "", pgProfile)
	idle, _, rel, _ := p.checkout(k, f.open)
	rel(true)
	busy, _, releaseBusy, _ := p.checkout(key(t, "pg", "x", pgProfile), f.open)

	p.close()
	if idle.(*fakeConn).closes.Load() != 1 {
		t.Error("idle connection not closed on shutdown")
	}
	releaseBusy(true)
	if busy.(*fakeConn).closes.Load() != 1 {
		t.Error("connection released after shutdown was kept")
	}
}

// TestPoolConcurrentCheckouts runs many calls on one key at once (run with
// -race): a connection is never checked out by two calls at the same time,
// and the pool stays within its bound.
func TestPoolConcurrentCheckouts(t *testing.T) {
	p := newConnPool(time.Minute, 3, time.Now)
	f := &fakeOpener{}
	k := key(t, "pg", "", pgProfile)
	var inUse sync.Map
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				c, _, rel, err := p.checkout(k, f.open)
				if err != nil {
					t.Error(err)
					return
				}
				if _, busy := inUse.LoadOrStore(c, true); busy {
					t.Errorf("connection %d checked out twice at once", c.(*fakeConn).id)
				}
				if c.(*fakeConn).closes.Load() != 0 {
					t.Errorf("connection %d handed out after it was closed", c.(*fakeConn).id)
				}
				inUse.Delete(c)
				rel(i%7 != 0)
			}
		}()
	}
	wg.Wait()
	if p.idleCount() > 3 {
		t.Errorf("idle = %d, over the bound 3", p.idleCount())
	}
	if f.count() >= 16*200 {
		t.Errorf("opened %d connections for %d checkouts: nothing was reused", f.count(), 16*200)
	}
}

func TestOpenedClose(t *testing.T) {
	cases := []struct {
		name      string
		resets    bool
		statement bool
		resetErr  error
		wantKeep  bool
		wantReset bool
	}{
		{"catalog call on a non-resetting adapter is kept unreset", false, false, nil, true, false},
		{"statement on a non-resetting adapter closes", false, true, nil, false, false},
		{"statement on a resetting adapter resets and keeps", true, true, nil, true, true},
		{"failed reset closes", true, true, errors.New("broken pipe"), false, true},
		{"catalog call on a resetting adapter is kept unreset", true, false, nil, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var kept *bool
			reset := false
			o := &opened{
				conn:    &fakeConn{},
				resets:  c.resets,
				reset:   func(context.Context, adapters.DB) error { reset = true; return c.resetErr },
				release: func(keep bool) { kept = &keep },
			}
			if c.statement {
				o.ranStatement()
			}
			o.close()
			if kept == nil || *kept != c.wantKeep {
				t.Errorf("keep = %v, want %v", kept, c.wantKeep)
			}
			if reset != c.wantReset {
				t.Errorf("reset ran = %v, want %v", reset, c.wantReset)
			}
		})
	}
}
