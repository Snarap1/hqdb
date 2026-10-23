package ui

import (
	"errors"
	"testing"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// fakeDB is a minimal adapters.DB stub so connPool tests never touch a real
// driver or network.
type fakeDB struct {
	id     int
	closed bool
}

func (f *fakeDB) Kind() string { return "fake" }
func (f *fakeDB) Close()       { f.closed = true }

// countingOpener returns a new *fakeDB each call and records how many times
// it was invoked, so tests can tell a cached connection from a fresh one.
func countingOpener(err error) (opener func(string, string) (adapters.DB, error), calls *int) {
	n := 0
	calls = &n
	opener = func(string, string) (adapters.DB, error) {
		n++
		if err != nil {
			return nil, err
		}
		return &fakeDB{id: n}, nil
	}
	return opener, calls
}

func newTestPool(opener func(string, string) (adapters.DB, error)) *connPool {
	return &connPool{conns: map[poolKey]adapters.DB{}, open: opener}
}

func TestConnPoolOpenCachesConnection(t *testing.T) {
	opener, calls := countingOpener(nil)
	p := newTestPool(opener)

	c1, err := p.Open(poolKey{profile: "pg-dev"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := p.Open(poolKey{profile: "pg-dev"})
	if err != nil {
		t.Fatal(err)
	}
	if c1 != c2 {
		t.Errorf("Open returned different connections for the same profile")
	}
	if *calls != 1 {
		t.Errorf("opener called %d times, want 1", *calls)
	}
}

func TestConnPoolOpenPropagatesError(t *testing.T) {
	wantErr := errors.New("dial tcp: refused")
	opener, _ := countingOpener(wantErr)
	p := newTestPool(opener)

	_, err := p.Open(poolKey{profile: "dead-dev"})
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestConnPoolReopenClosesOldAndOpensFresh(t *testing.T) {
	opener, calls := countingOpener(nil)
	p := newTestPool(opener)

	first, _ := p.Open(poolKey{profile: "pg-dev"})
	second, err := p.Reopen(poolKey{profile: "pg-dev"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.(*fakeDB).closed {
		t.Errorf("Reopen did not close the old connection")
	}
	if first == second {
		t.Errorf("Reopen returned the same connection")
	}
	if *calls != 2 {
		t.Errorf("opener called %d times, want 2", *calls)
	}
}

func TestConnPoolDropClosesAndNextOpenIsFresh(t *testing.T) {
	opener, calls := countingOpener(nil)
	p := newTestPool(opener)

	first, _ := p.Open(poolKey{profile: "pg-dev"})
	p.Drop("pg-dev")
	if !first.(*fakeDB).closed {
		t.Errorf("Drop did not close the connection")
	}
	second, _ := p.Open(poolKey{profile: "pg-dev"})
	if first == second {
		t.Errorf("Open after Drop returned the stale connection")
	}
	if *calls != 2 {
		t.Errorf("opener called %d times, want 2", *calls)
	}
}

func TestConnPoolDropOfUnknownProfileIsNoop(t *testing.T) {
	opener, _ := countingOpener(nil)
	p := newTestPool(opener)
	p.Drop("never-opened") // must not panic
}

func TestConnPoolKeysConnectionsByProfileAndDatabase(t *testing.T) {
	opener, calls := countingOpener(nil)
	p := newTestPool(opener)

	a, _ := p.Open(poolKey{"pg-multi", "one"})
	b, _ := p.Open(poolKey{"pg-multi", "two"})
	a2, _ := p.Open(poolKey{"pg-multi", "one"})
	if a == b {
		t.Errorf("two databases of one Profile shared a connection")
	}
	if a != a2 {
		t.Errorf("same (Profile, database) opened twice")
	}
	if *calls != 2 {
		t.Errorf("opener called %d times, want 2", *calls)
	}
}

func TestConnPoolReopenTouchesOnlyThatDatabase(t *testing.T) {
	opener, _ := countingOpener(nil)
	p := newTestPool(opener)

	a, _ := p.Open(poolKey{"pg-multi", "one"})
	b, _ := p.Open(poolKey{"pg-multi", "two"})
	a2, err := p.Reopen(poolKey{"pg-multi", "one"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.(*fakeDB).closed || a2 == a {
		t.Errorf("Reopen did not replace the connection")
	}
	if b.(*fakeDB).closed {
		t.Errorf("Reopen closed another database's connection")
	}
}

func TestConnPoolDropClosesEveryDatabaseOfTheProfile(t *testing.T) {
	opener, _ := countingOpener(nil)
	p := newTestPool(opener)

	a, _ := p.Open(poolKey{"pg-multi", "one"})
	b, _ := p.Open(poolKey{"pg-multi", "two"})
	other, _ := p.Open(poolKey{"pg-dev", ""})
	p.Drop("pg-multi")
	if !a.(*fakeDB).closed || !b.(*fakeDB).closed {
		t.Errorf("Drop left a database connection open")
	}
	if other.(*fakeDB).closed {
		t.Errorf("Drop closed another Profile's connection")
	}
}
