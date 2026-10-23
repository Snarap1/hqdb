package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/consolestate"
	"github.com/Snarap1/hqdb/internal/rediscmd"
)

// redisConsoleTab is a Redis Console: the SQL Console's sibling for a
// redis Profile. It shares the consoleCore (editor, persisted history,
// write confirmation, Ctrl+C cancellation, layout) and the grid, but runs
// one command line (the line under the cursor) through rediscmd.Run on
// its own client instead of statements on a pinned SQL session. A
// command the MCP whitelist refuses is refused on a read-only Profile
// and asks on a read-write one (ADR-0003).
type redisConsoleTab struct {
	consoleCore
	hasGrid bool
	grid    grid
	conn    *redisConsoleConn
}

// redisConsoleConn is a Redis Console's own client, opened lazily outside
// the pool (like the SQL Console's session) so that cancelling a blocked
// command can close it without touching the Catalog tree's connection:
// go-redis does not interrupt a blocking read on context cancellation, so
// run closes the client when ctx is cancelled, which unblocks the read,
// and the next command opens a fresh one.
type redisConsoleConn struct {
	mu      sync.Mutex
	profile string
	db      adapters.DB
	closed  bool
}

// client returns the open client, opening it first if needed.
func (c *redisConsoleConn) client(pool *connPool) (adapters.DB, *adapters.Redis, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, fmt.Errorf("console is closed")
	}
	if c.db == nil {
		db, err := pool.OpenDedicated(poolKey{profile: c.profile})
		if err != nil {
			return nil, nil, err
		}
		if _, ok := db.(*adapters.Redis); !ok {
			db.Close()
			return nil, nil, fmt.Errorf("profile %q is not redis", c.profile)
		}
		c.db = db
	}
	return c.db, c.db.(*adapters.Redis), nil
}

// drop closes db if it is still the open client.
func (c *redisConsoleConn) drop(db adapters.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == db {
		c.db = nil
		db.Close()
	}
}

// run executes line, unscreened, closing the client if ctx is cancelled
// while it runs.
func (c *redisConsoleConn) run(ctx context.Context, pool *connPool, line string) (any, error) {
	db, rc, err := c.client(pool)
	if err != nil {
		return nil, err
	}
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			c.drop(db)
		case <-finished:
		}
	}()
	return rediscmd.Run(ctx, rc, line, nil)
}

// close releases the client; later commands fail instead of reopening.
func (c *redisConsoleConn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.db != nil {
		c.db.Close()
		c.db = nil
	}
}

func newRedisConsoleTab(profile string, state *consolestate.Store) *redisConsoleTab {
	t := &redisConsoleTab{conn: &redisConsoleConn{profile: profile}}
	t.init(profile, "REDIS", state)
	return t
}

// Close releases the Console's own client when its tab closes.
func (t *redisConsoleTab) Close() { t.conn.close() }

func (t *redisConsoleTab) EditingText() bool { return t.editingText(t.grid.modal()) }

// redisCommandAt returns the trimmed line of text holding byte offset
// cur: a Redis Console runs one command line, so the line is the unit.
func redisCommandAt(text string, cur int) string {
	start := strings.LastIndexByte(text[:cur], '\n') + 1
	end := len(text)
	if i := strings.IndexByte(text[cur:], '\n'); i >= 0 {
		end = cur + i
	}
	return strings.TrimSpace(text[start:end])
}

// redisReplyGrid lays a JSON-safe redis reply out for the shared grid: a
// map as field/value rows (sorted by field), an array as index/value
// rows, and anything else as a single "reply" cell. Nested values show
// as JSON. A nil reply has no grid.
func redisReplyGrid(reply any) (columns []string, rows [][]any, ok bool) {
	cell := func(v any) any {
		switch v.(type) {
		case []any, map[string]any:
			b, err := json.Marshal(v)
			if err == nil {
				return string(b)
			}
		}
		return v
	}
	switch r := reply.(type) {
	case nil:
		return nil, nil, false
	case map[string]any:
		fields := make([]string, 0, len(r))
		for f := range r {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		for _, f := range fields {
			rows = append(rows, []any{f, cell(r[f])})
		}
		return []string{"field", "value"}, rows, true
	case []any:
		for i, v := range r {
			rows = append(rows, []any{i, cell(v)})
		}
		return []string{"index", "value"}, rows, true
	}
	return []string{"reply"}, [][]any{{reply}}, true
}

// redisConsoleMsg reports one command's outcome to the Console that ran it.
type redisConsoleMsg struct {
	tab      *redisConsoleTab
	reply    any
	duration time.Duration
	err      error
	// cancelled is set when Ctrl+C stopped the command.
	cancelled bool
}

func applyRedisConsoleMsg(msg redisConsoleMsg) {
	t := msg.tab
	t.hasGrid = false
	if !t.finish(msg.err, msg.cancelled, "command cancelled") {
		return
	}
	t.status = "reply in " + formatDuration(msg.duration)
	cols, rows, ok := redisReplyGrid(msg.reply)
	if !ok {
		t.status = "(nil) in " + formatDuration(msg.duration)
		return
	}
	t.grid, t.hasGrid = newGrid(cols, rows), true
}

// runCmd runs the command line under the cursor, asking first when it
// would write.
func (t *redisConsoleTab) runCmd(pool *connPool) tea.Cmd {
	line := t.statementAt(redisCommandAt)
	if line == "" {
		return nil
	}
	return t.runOrAsk(line, rediscmd.CommandViolation(line), func() tea.Cmd { return t.execCmd(pool, line) })
}

// execCmd runs line on the Console's own redis client, unscreened.
func (t *redisConsoleTab) execCmd(pool *connPool, line string) tea.Cmd {
	conn := t.conn
	return t.exec(line, func(ctx context.Context) tea.Msg {
		start := time.Now()
		reply, err := conn.run(ctx, pool, line)
		return redisConsoleMsg{tab: t, reply: reply, duration: time.Since(start), err: err, cancelled: ctx.Err() != nil}
	})
}

func (t *redisConsoleTab) Update(msg tea.KeyPressMsg, keys keyMap, pool *connPool) tea.Cmd {
	if cmd, ok := t.updateFront(msg, keys, t.grid.modal(), func() tea.Cmd { return t.runCmd(pool) }); ok {
		return cmd
	}
	if t.hasGrid {
		var handled bool
		if t.grid, handled = t.grid.update(msg, keys); handled {
			return nil
		}
	}
	if key.Matches(msg, keys.EditBuffer) {
		t.editing = true
	}
	return nil
}

// CopyText is the current reply cell, for the global copy key.
func (t *redisConsoleTab) CopyText() (string, bool) {
	if !t.hasGrid {
		return "", false
	}
	return t.grid.cellCopyText()
}

// CopyRowText is the current reply row as JSON, for the row copy key.
func (t *redisConsoleTab) CopyRowText() (string, bool) {
	if !t.hasGrid {
		return "", false
	}
	return t.grid.rowCopyText()
}

// ClickCell mirrors consoleTab.ClickCell over the same layout.
func (t *redisConsoleTab) ClickCell(x, y, width, height int, double bool) {
	if t.grid.modal() {
		return
	}
	if top, ok := t.clickEditor(y, height); ok && t.hasGrid {
		t.grid = clickGrid(t.grid, x, y, top, width, height, double)
	}
}

func (t *redisConsoleTab) Help(keys keyMap) []key.Binding {
	if t.editing {
		return t.editorHelp(keys)
	}
	h := t.resultsHelpHead(keys)
	if t.hasGrid {
		h = append(h, keys.gridHelp()...)
	}
	return h
}

func (t *redisConsoleTab) View(width, height int, focused bool) string {
	status := t.statusLine(width, func() string {
		s := t.status
		if st := t.grid.searchStatus(); t.hasGrid && st != "" {
			s += "  " + st
		}
		return s
	})
	var results func(int) string
	if t.showResults(t.hasGrid) {
		results = func(h int) string { return t.grid.view(width, h, focused && !t.editing) }
	}
	return t.frame("redis console", status, width, height, focused, results)
}

func (t *redisConsoleTab) cellPopupOpen() bool { return t.hasGrid && t.grid.popupOpen() }
