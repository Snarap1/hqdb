package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/values"
)

// redisValueCap bounds how many elements of a collection-typed key the
// value tab loads; the header reports the full length next to it, so a
// truncated view never passes for the whole value.
const redisValueCap = 1000

// redisKeyTab shows one redis key's value according to its type (spec:
// user stories 57-59): a string as text, every other type in the shared
// grid (hash: field/value, list: index/value, set: member, sorted set:
// member/score, stream: id/fields), with the key's type and TTL above it.
type redisKeyTab struct {
	title   string
	profile string
	key     string
	loading bool
	err     error
	val     *redisValue
	grid    grid
	// cancelled is set when Ctrl+C stopped the load.
	cancelled bool
	work      cancelSet
}

// Cancel cancels the running key load (Ctrl+C).
func (t *redisKeyTab) Cancel() { t.work.cancelAll() }

// redisValue is one loaded key.
type redisValue struct {
	typ    string
	ttl    time.Duration // as go-redis reports it: -1 no expiry, -2 missing
	length int64         // full element count (string: byte length)
	text   string        // the value of a string key
	// columns and rows hold every other type's value, for the grid.
	columns []string
	rows    [][]any
}

// redisKeyMsg reports the result of loading a redisKeyTab.
type redisKeyMsg struct {
	tab       *redisKeyTab
	val       *redisValue
	err       error
	cancelled bool
}

// openRedisKeyCmd starts loading the key whose Catalog path (separator-
// split segments) is path on profile, returning the tab in its loading
// state and the tea.Cmd that fills it. Like openDDLCmd, a failure on the
// cached connection is retried once after Reopen.
func openRedisKeyCmd(pool *connPool, profile string, path []string) (*redisKeyTab, tea.Cmd) {
	key := strings.Join(path, ":")
	t := &redisKeyTab{title: profile + ": " + key, profile: profile, key: key, loading: true}
	ctx, done := t.work.start()
	cmd := func() tea.Msg {
		defer done()
		load := func(conn adapters.DB) (*redisValue, error) {
			rc, ok := conn.(*adapters.Redis)
			if !ok {
				return nil, fmt.Errorf("key values are not available for %s", conn.Kind())
			}
			return loadRedisValue(ctx, rc.Redis(), strings.Join(path, rc.Sep()))
		}
		conn, err := pool.Open(profile)
		var val *redisValue
		if err == nil {
			val, err = load(conn)
		}
		if err != nil && ctx.Err() == nil {
			if conn2, rerr := pool.Reopen(profile); rerr == nil {
				val, err = load(conn2)
			}
		}
		return redisKeyMsg{tab: t, val: val, err: err, cancelled: ctx.Err() != nil}
	}
	return t, cmd
}

// applyRedisKeyMsg writes an openRedisKeyCmd result into its tab. The
// title is fixed when the tab opens, joined with ":", the common separator;
// the key is read with the Profile's real separator.
func applyRedisKeyMsg(msg redisKeyMsg) {
	t := msg.tab
	t.loading = false
	if msg.cancelled {
		t.cancelled = true
		return
	}
	t.err = msg.err
	t.val = msg.val
	if msg.val != nil && msg.val.columns != nil {
		t.grid = newGrid(msg.val.columns, msg.val.rows)
	}
}

// loadRedisValue reads key's type, TTL, length, and (capped) value. Every
// command used is a plain read.
func loadRedisValue(ctx context.Context, rc *redis.Client, key string) (*redisValue, error) {
	typ, err := rc.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	ttl, err := rc.TTL(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	v := &redisValue{typ: typ, ttl: ttl}
	cell := func(s string) any { return values.JSON([]byte(s)) }
	switch typ {
	case "none":
		return nil, fmt.Errorf("key %q does not exist", key)
	case "string":
		s, err := rc.Get(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		v.text, v.length = fmt.Sprint(cell(s)), int64(len(s))
	case "hash":
		if v.length, err = rc.HLen(ctx, key).Result(); err != nil {
			return nil, err
		}
		v.columns = []string{"field", "value"}
		err = scanAll(func(cur uint64) ([]string, uint64, error) {
			return rc.HScan(ctx, key, cur, "*", redisScanCount).Result()
		}, 2, func(kv []string) { v.rows = append(v.rows, []any{cell(kv[0]), cell(kv[1])}) }, &v.rows)
	case "list":
		if v.length, err = rc.LLen(ctx, key).Result(); err != nil {
			return nil, err
		}
		items, err := rc.LRange(ctx, key, 0, redisValueCap-1).Result()
		if err != nil {
			return nil, err
		}
		v.columns = []string{"index", "value"}
		for i, it := range items {
			v.rows = append(v.rows, []any{i, cell(it)})
		}
	case "set":
		if v.length, err = rc.SCard(ctx, key).Result(); err != nil {
			return nil, err
		}
		v.columns = []string{"member"}
		err = scanAll(func(cur uint64) ([]string, uint64, error) {
			return rc.SScan(ctx, key, cur, "*", redisScanCount).Result()
		}, 1, func(m []string) { v.rows = append(v.rows, []any{cell(m[0])}) }, &v.rows)
	case "zset":
		if v.length, err = rc.ZCard(ctx, key).Result(); err != nil {
			return nil, err
		}
		zs, err := rc.ZRangeWithScores(ctx, key, 0, redisValueCap-1).Result()
		if err != nil {
			return nil, err
		}
		v.columns = []string{"member", "score"}
		for _, z := range zs {
			v.rows = append(v.rows, []any{cell(fmt.Sprint(z.Member)), z.Score})
		}
	case "stream":
		if v.length, err = rc.XLen(ctx, key).Result(); err != nil {
			return nil, err
		}
		msgs, err := rc.XRangeN(ctx, key, "-", "+", redisValueCap).Result()
		if err != nil {
			return nil, err
		}
		v.columns = []string{"id", "fields"}
		for _, m := range msgs {
			b, err := json.Marshal(values.JSON(m.Values))
			if err != nil {
				return nil, err
			}
			v.rows = append(v.rows, []any{m.ID, string(b)})
		}
	default:
		return nil, fmt.Errorf("key %q has type %s, which has no value view", key, typ)
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// redisScanCount is the COUNT hint for HSCAN/SSCAN.
const redisScanCount = 500

// scanAll drives a cursor-based scan (HSCAN, SSCAN) until it wraps or
// rows reaches redisValueCap, handing each group of width replies to add.
// A scan may return an element more than once; the value view tolerates
// that rather than buffering a dedupe set for very large keys.
func scanAll(step func(uint64) ([]string, uint64, error), width int, add func([]string), rows *[][]any) error {
	var cur uint64
	for {
		batch, next, err := step(cur)
		if err != nil {
			return err
		}
		for i := 0; i+width <= len(batch) && len(*rows) < redisValueCap; i += width {
			add(batch[i : i+width])
		}
		cur = next
		if cur == 0 || len(*rows) >= redisValueCap {
			return nil
		}
	}
}

// ttlText renders a TTL as the header shows it.
func ttlText(ttl time.Duration) string {
	switch {
	case ttl == -1:
		return "no expiry"
	case ttl < 0:
		return "key does not exist"
	default:
		return fmt.Sprintf("%ds", int64(ttl/time.Second))
	}
}

// header is the line above the value: type, TTL, and length, with how
// much was loaded when a collection was cut at redisValueCap.
func (v *redisValue) header() string {
	field := func(label, value string, c lipgloss.Style) string {
		return mutedStyle.Render(label) + " " + c.Render(value)
	}
	plain := lipgloss.NewStyle().Foreground(colorText)
	ttl := plain
	if v.ttl >= 0 {
		ttl = accentStyle
	}
	s := field("type", v.typ, lipgloss.NewStyle().Foreground(colorPurple)) + "   " +
		field("TTL", ttlText(v.ttl), ttl) + "   " +
		field("length", fmt.Sprint(v.length), plain)
	if v.columns != nil && int64(len(v.rows)) < v.length {
		s += mutedStyle.Render(fmt.Sprintf(" (showing first %d)", len(v.rows)))
	}
	return s
}

func (t *redisKeyTab) Title() string { return t.title }

func (t *redisKeyTab) Heading() tabHeading {
	return tabHeading{tag: "KEY", profile: t.profile, object: t.key}
}

func (t *redisKeyTab) EditingText() bool { return t.grid.modal() }

func (t *redisKeyTab) isGrid() bool { return t.val != nil && t.val.columns != nil }

// CopyText is a string key's whole value, or a grid's current cell.
func (t *redisKeyTab) CopyText() (string, bool) {
	switch {
	case t.isGrid():
		return t.grid.cellCopyText()
	case t.val != nil:
		return t.val.text, true
	}
	return "", false
}

// CopyRowText is the grid's current row as JSON.
func (t *redisKeyTab) CopyRowText() (string, bool) {
	if !t.isGrid() {
		return "", false
	}
	return t.grid.rowCopyText()
}

// redisKeyGridTop is how many lines sit above the grid: the header and the
// search status line.
const redisKeyGridTop = 2

// ClickCell moves the cell cursor, or opens the popup on a double-click.
func (t *redisKeyTab) ClickCell(x, y, width, height int, double bool) {
	if !t.isGrid() || t.grid.modal() {
		return
	}
	r, c, ok := t.grid.cellAt(x, y-redisKeyGridTop, width, height-redisKeyGridTop)
	if !ok {
		return
	}
	if double {
		t.grid = t.grid.openPopupAt(r, c)
		return
	}
	t.grid.cursorRow, t.grid.cursorCol = r, c
}

func (t *redisKeyTab) Help(keys keyMap) []key.Binding {
	if t.isGrid() {
		return []key.Binding{keys.CellPopup, keys.Copy, keys.CopyRow, keys.Search}
	}
	return []key.Binding{keys.Copy}
}

func (t *redisKeyTab) Update(k tea.KeyPressMsg, keys keyMap, _ *connPool) tea.Cmd {
	if t.isGrid() {
		t.grid, _ = t.grid.update(k, keys)
	}
	return nil
}

func (t *redisKeyTab) View(width, height int, focused bool) string {
	header := headerLine(width, t.key, t.profile, "")
	switch {
	case t.loading:
		return header + "\n" + mutedStyle.Render("Loading...")
	case t.cancelled:
		return header + "\n" + accentStyle.Render(t.title+": load cancelled")
	case t.err != nil:
		return header + "\n\n" + errorStyle.Width(max(width, 1)).Render(fmt.Sprintf("%s failed to load:\n\n%v", t.title, t.err))
	}
	if !t.isGrid() {
		return header + "\n" + t.val.header() + "\n\n" + lipgloss.NewStyle().Width(max(width, 1)).Render(t.val.text)
	}
	meta := spread(width, t.val.header(), mutedStyle.Render(t.grid.searchStatus()))
	return header + "\n" + meta + "\n" + t.grid.view(width, height-redisKeyGridTop, focused)
}

func (t *redisKeyTab) cellPopupOpen() bool { return t.isGrid() && t.grid.popupOpen() }
