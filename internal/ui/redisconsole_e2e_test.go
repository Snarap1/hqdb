package ui_test

import (
	"bytes"
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// runRedisLine types line on a new line of the focused Redis Console
// editor and runs it with Alt+Enter.
func runRedisLine(tm *teatest.TestModel, line string) {
	enter(tm)
	typeText(tm, line)
	altEnter(tm)
}

func TestRedisConsoleRunsReadsFreelyAndConfirmsWrites(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	p, err := cfg.Profile("redis-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(p)
	if err != nil {
		t.Fatalf("connecting to redis-dev: %v", err)
	}
	rc := conn.(*adapters.Redis).Redis()
	ctx := context.Background()
	const str, hash, written = "aaatui18:str", "aaatui18:hash", "aaatui18:written"
	keys := []string{str, hash, written}
	t.Cleanup(func() {
		rc.Del(ctx, keys...)
		conn.Close()
	})
	rc.Del(ctx, keys...)
	if err := rc.Set(ctx, str, "console-str-val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rc.HSet(ctx, hash, "hf1", "hv1").Err(); err != nil {
		t.Fatal(err)
	}

	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "redis-dev")
	selectNode(t, tm, "redis-dev")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitForFrameContaining(t, tm.Output(), "redis-dev  redis console")

	typeText(tm, "GET "+str)
	altEnter(tm)
	waitForFrameContaining(t, tm.Output(), "console-str-val")

	runRedisLine(tm, "HGETALL "+hash)
	waitForFrame(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("hf1")) && bytes.Contains(b, []byte("hv1"))
	})

	runRedisLine(tm, "SCAN 0 MATCH aaatui18:s* COUNT 100000")
	waitForFrameContaining(t, tm.Output(), `["`+str+`"]`)

	// A redis error shows in the result area.
	runRedisLine(tm, "HGETALL "+str)
	waitForFrameContaining(t, tm.Output(), "Error: WRONGTYPE")

	// SET asks; "n" cancels and nothing is written.
	runRedisLine(tm, "SET "+written+" yes")
	waitForFrameContaining(t, tm.Output(), "This will write to redis-dev")
	tm.Send(tea.KeyPressMsg{Code: 'n', Text: "n"})
	waitForFrameContaining(t, tm.Output(), "Cancelled")
	if n := rc.Exists(ctx, written).Val(); n != 0 {
		t.Fatalf("SET ran despite n")
	}

	// "y" runs it; the key is then visible from the Console.
	altEnter(tm)
	confirmWrite(t, tm)
	waitForFrameContaining(t, tm.Output(), "OK")
	runRedisLine(tm, "GET "+written)
	waitForFrameContaining(t, tm.Output(), "yes")
	if v := rc.Get(ctx, written).Val(); v != "yes" {
		t.Fatalf("written key = %q, want yes", v)
	}

	// A blocking command runs until Ctrl+C cancels it; the Console stays
	// usable afterwards.
	runRedisLine(tm, "BLPOP aaatui18:never 0")
	confirmWrite(t, tm)
	waitForFrameContaining(t, tm.Output(), "Running...")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	waitForFrameContaining(t, tm.Output(), "command cancelled")
	runRedisLine(tm, "GET "+str)
	waitForFrameContaining(t, tm.Output(), "console-str-val")
}
