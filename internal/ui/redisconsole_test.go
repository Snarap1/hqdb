package ui

import (
	"reflect"
	"testing"
)

func TestRedisCommandAtPicksTheLineUnderTheCursor(t *testing.T) {
	text := "GET a\n  HGETALL b  \nSCAN 0"
	cases := []struct {
		cur  int
		want string
	}{{0, "GET a"}, {5, "GET a"}, {8, "HGETALL b"}, {len(text), "SCAN 0"}}
	for _, c := range cases {
		if got := redisCommandAt(text, c.cur); got != c.want {
			t.Errorf("redisCommandAt(%d) = %q, want %q", c.cur, got, c.want)
		}
	}
}

func TestRedisReplyGrid(t *testing.T) {
	cases := []struct {
		name  string
		reply any
		cols  []string
		rows  [][]any
		ok    bool
	}{
		{"nil", nil, nil, nil, false},
		{"scalar", "v", []string{"reply"}, [][]any{{"v"}}, true},
		{"map sorted", map[string]any{"b": "2", "a": "1"}, []string{"field", "value"}, [][]any{{"a", "1"}, {"b", "2"}}, true},
		{"array nested as json", []any{"0", []any{"k1", "k2"}}, []string{"index", "value"}, [][]any{{0, "0"}, {1, `["k1","k2"]`}}, true},
	}
	for _, c := range cases {
		cols, rows, ok := redisReplyGrid(c.reply)
		if ok != c.ok || !reflect.DeepEqual(cols, c.cols) || !reflect.DeepEqual(rows, c.rows) {
			t.Errorf("%s: got %v %v %v", c.name, cols, rows, ok)
		}
	}
}
