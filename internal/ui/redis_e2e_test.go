package ui_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/config"
)

// redisKeysPrefix is the Key prefix the redis value test seeds its keys
// under; the test selects it, and each key, by name.
const redisKeysPrefix = "aaatui17"

// seedRedisKeys writes one key of each redis type under redisKeysPrefix
// and deletes them all when the test ends.
func seedRedisKeys(t *testing.T, cfg *config.Config) {
	t.Helper()
	p, err := cfg.Profile("redis-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(p)
	if err != nil {
		t.Fatalf("connecting to redis-dev to seed keys: %v", err)
	}
	rc := conn.(*adapters.Redis).Redis()
	ctx := context.Background()
	k := func(s string) string { return redisKeysPrefix + ":" + s }
	keys := []string{k("str"), k("ttl"), k("hash"), k("list"), k("set"), k("zset"), k("stream")}
	t.Cleanup(func() {
		rc.Del(ctx, keys...)
		conn.Close()
	})
	rc.Del(ctx, keys...)
	for _, err := range []error{
		rc.Set(ctx, k("str"), "hello-redis", 0).Err(),
		rc.Set(ctx, k("ttl"), "short-lived", time.Hour).Err(),
		rc.HSet(ctx, k("hash"), "f1", "v1", "f2", "v2").Err(),
		rc.RPush(ctx, k("list"), "item-a", "item-b").Err(),
		rc.SAdd(ctx, k("set"), "m-x").Err(),
		rc.ZAdd(ctx, k("zset"), redis.Z{Score: 2.5, Member: "zm"}).Err(),
		rc.XAdd(ctx, &redis.XAddArgs{Stream: k("stream"), ID: "1-1", Values: []any{"k", "sv"}}).Err(),
	} {
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestRedisTreeExpandsKeyPrefixesAndOpensEachKeyType(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	seedRedisKeys(t, cfg)
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "redis-dev")

	selectNode(t, tm, "redis-dev")
	enter(tm)
	frame := waitForFrame(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(redisKeysPrefix+"  prefix")) && bytes.Contains(b, []byte("plainkey  key"))
	})
	if !bytes.Contains(frame, []byte("user  prefix")) {
		t.Errorf("frame missing user prefix:\n%s", frame)
	}

	selectNode(t, tm, redisKeysPrefix)
	enter(tm) // aaatui17 -> hash, list, set, str, stream, ttl, zset
	waitForFrameContaining(t, tm.Output(), "zset  key")

	cases := []struct {
		key  string
		want []string
	}{
		{"hash", []string{"KEY redis-dev " + redisKeysPrefix + ":hash", "type hash", "TTL no expiry", "field", "f1", "v2"}},
		{"list", []string{"list", "index", "item-a", "item-b"}},
		{"set", []string{"member", "m-x"}},
		{"str", []string{"hello-redis"}},
		{"stream", []string{"fields", "1-1", `"k":"sv"`}},
		{"ttl", []string{"TTL 3", "short-lived"}},
		{"zset", []string{"score", "zm", "2.5"}},
	}
	// Bubble Tea redraws only what changed, so each case asserts on what
	// differs from the previous key's tab (see waitForFrame).
	for i, c := range cases {
		selectNode(t, tm, c.key)
		if i == 0 {
			frame := waitForFrameContaining(t, tm.Output(), "▌")
			if bytes.Contains(frame, []byte("d ddl")) {
				t.Errorf("status bar must not hint d on a redis key:\n%s", frame)
			}
		}
		enter(tm)
		waitForFrame(t, tm.Output(), func(b []byte) bool {
			for _, w := range c.want {
				if !bytes.Contains(b, []byte(w)) {
					return false
				}
			}
			return true
		})
		tm.Send(tea.KeyPressMsg{Code: tea.KeyTab}) // focus back to the tree
	}
}

func TestRedisKeyNestedUnderAPrefixThatIsAlsoAKey(t *testing.T) {
	// The fixture's "user:1000" is both a key and the prefix of
	// "user:1000:roles", so the "user" level offers both rows.
	cfg := loadFixtureConfig(t, "")
	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "redis-dev")
	selectNode(t, tm, "redis-dev")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "user  prefix")
	selectNode(t, tm, "user")
	enter(tm)
	waitForFrame(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("1000  prefix")) && bytes.Contains(b, []byte("1000  key"))
	})
}
