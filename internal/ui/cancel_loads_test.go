package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// blockingPool is a connPool whose connect blocks until release is closed
// and then fails, so a test can cancel work while it is in flight; opened
// is closed once the first connect starts.
func blockingPool() (pool *connPool, opened, release chan struct{}) {
	opened, release = make(chan struct{}), make(chan struct{})
	first := true
	pool = &connPool{conns: map[string]adapters.DB{}, open: func(string) (adapters.DB, error) {
		if first {
			first = false
			close(opened)
		}
		<-release
		return nil, errors.New("offline")
	}}
	return pool, opened, release
}

// runCancelled runs cmd, cancels via cancel once the connect is in
// flight, and returns the message cmd produced.
func runCancelled(t *testing.T, cmd tea.Cmd, opened, release chan struct{}, cancel func()) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no load command")
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	<-opened
	cancel()
	close(release)
	return <-out
}

func TestCtrlCCancelsAMongoPageLoad(t *testing.T) {
	pool, opened, release := blockingPool()
	tb := loadedMongoTab(sampleDocs()...)
	msg := runCancelled(t, tb.loadCmd(pool), opened, release, tb.Cancel).(mongoDataViewMsg)
	applyMongoDataViewMsg(msg)
	v := plainView(tb)
	if !strings.Contains(v, "page load cancelled") {
		t.Errorf("view must say the page load was cancelled:\n%s", v)
	}
	if !strings.Contains(v, `name: "bolt"`) || strings.Contains(v, "offline") {
		t.Errorf("the previous page must stay, with no error:\n%s", v)
	}
}

func TestCtrlCCancelsAMongoCount(t *testing.T) {
	pool, opened, release := blockingPool()
	tb := loadedMongoTab(sampleDocs()...)
	msg := runCancelled(t, tb.countCmd(pool), opened, release, tb.Cancel).(mongoCountMsg)
	applyMongoCountMsg(msg)
	v := plainView(tb)
	if !strings.Contains(v, "count cancelled") || strings.Contains(v, "counting") || strings.Contains(v, "offline") {
		t.Errorf("view must say the count was cancelled:\n%s", v)
	}
}

func TestCtrlCCancelsADDLLoad(t *testing.T) {
	pool, opened, release := blockingPool()
	tb, cmd := openDDLCmd(pool, "pg-dev", []string{"hqdb", "public", "widgets"})
	applyDDLMsg(runCancelled(t, cmd, opened, release, tb.Cancel).(ddlMsg))
	v := ansi.Strip(tb.View(100, 30, true))
	if !strings.Contains(v, "load cancelled") || strings.Contains(v, "offline") || strings.Contains(v, "Loading") {
		t.Errorf("view must say the DDL load was cancelled:\n%s", v)
	}
}

func TestCtrlCCancelsARedisKeyLoad(t *testing.T) {
	pool, opened, release := blockingPool()
	tb, cmd := openRedisKeyCmd(pool, "redis-dev", []string{"user", "1"})
	applyRedisKeyMsg(runCancelled(t, cmd, opened, release, tb.Cancel).(redisKeyMsg))
	v := ansi.Strip(tb.View(100, 30, true))
	if !strings.Contains(v, "load cancelled") || strings.Contains(v, "offline") {
		t.Errorf("view must say the key load was cancelled:\n%s", v)
	}
}

func TestLoadTabsAreCancellableFromTheModel(t *testing.T) {
	ddl, _ := openDDLCmd(nil, "pg-dev", []string{"hqdb", "public", "widgets"})
	key, _ := openRedisKeyCmd(nil, "redis-dev", []string{"k"})
	for _, tb := range []tab{newMongoTab("mongo-dev", []string{"hqdb", "w"}), ddl, key} {
		if _, ok := tb.(tabCanceller); !ok {
			t.Errorf("%T does not implement tabCanceller, so Ctrl+C cannot reach it", tb)
		}
	}
}
