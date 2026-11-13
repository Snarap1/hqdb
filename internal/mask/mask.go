// Package mask replaces personal data in what the MCP server returns with
// stable placeholders (ADR-0005).
// A value is masked when its column or field name matches a name rule, or
// when internal/mask/detect recognizes the value itself.
package mask

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"hash"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Snarap1/hqdb/internal/mask/detect"
	"github.com/Snarap1/hqdb/internal/values"
)

// Masker masks values under one key and one set of user name rules.
// A Masker is not safe for concurrent use: it caches name classes and
// reuses one HMAC. The server builds a fresh one per tool call.
type Masker struct {
	key     []byte
	columns []string
	allow   []string

	names map[string]nameClass // classify results by single name
	paths map[string]nameClass // classify results by pathKey
	mac   hash.Hash            // HMAC-SHA256 under key, reset per marker
	sum   []byte
}

// maxCachedClasses bounds each classify cache; a Document stream with
// unbounded distinct field names stops caching at this size.
const maxCachedClasses = 4096

// NewKey returns 32 random bytes. The server creates it once per process
// and never writes it anywhere.
func NewKey() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(fmt.Sprintf("mask: reading random key: %v", err))
	}
	return k
}

// New validates the [masking] table and returns a Masker. A nil map means
// the built-in rules only.
func New(masking map[string]any, key []byte) (*Masker, error) {
	m := &Masker{key: key}
	for _, k := range sortedKeys(masking) {
		if k != "columns" && k != "allow" {
			return nil, fmt.Errorf("unknown option %q in [masking]; valid options: columns, allow", k)
		}
	}
	var err error
	if m.columns, err = globList(masking, "columns"); err != nil {
		return nil, err
	}
	if m.allow, err = globList(masking, "allow"); err != nil {
		return nil, err
	}
	return m, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// globList reads one string-array option and checks every glob.
func globList(masking map[string]any, name string) ([]string, error) {
	raw, ok := masking[name]
	if !ok {
		return nil, nil
	}
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case []string:
		for _, s := range v {
			items = append(items, s)
		}
	default:
		return nil, fmt.Errorf("[masking] %s must be an array of strings", name)
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, fmt.Errorf("[masking] %s[%d] must be a string", name, i)
		}
		if _, err := path.Match(s, ""); err != nil {
			return nil, fmt.Errorf("[masking] %s: malformed glob %q", name, s)
		}
		out = append(out, s)
	}
	return out, nil
}

type nameClass int

const (
	classNone nameClass = iota
	classNamed
	classSpecial
)

// classify applies the name rules to a field path: the last segment and
// the whole dotted path are both tried. allow wins over everything.
func (m *Masker) classify(segs []string) nameClass {
	if len(segs) == 0 {
		return classNone
	}
	cache, key := &m.names, segs[0]
	if len(segs) > 1 {
		cache, key = &m.paths, pathKey(segs)
	}
	if c, ok := (*cache)[key]; ok {
		return c
	}
	c := m.classifyNames(segs)
	if *cache == nil {
		*cache = map[string]nameClass{}
	}
	if len(*cache) < maxCachedClasses {
		(*cache)[key] = c
	}
	return c
}

// pathKey is the cache key of a path of two or more segments: each
// segment prefixed with its length and a NUL, so two different paths
// never share a key.
func pathKey(segs []string) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(0)
		b.WriteString(s)
	}
	return b.String()
}

func (m *Masker) classifyNames(segs []string) nameClass {
	norms := make([]string, len(segs))
	for i, s := range segs {
		norms[i] = normalizeName(s)
	}
	last := norms[len(norms)-1]
	dotted := strings.Join(norms, ".")
	tokenForms := []string{last}
	globForms := []string{last}
	if len(segs) > 1 {
		tokenForms = append(tokenForms, strings.Join(norms, "_"))
		globForms = append(globForms, dotted)
	}
	if globsMatch(m.allow, globForms) {
		return classNone
	}
	for _, f := range tokenForms {
		if specialSet.matches(f) {
			return classSpecial
		}
	}
	if globsMatch(m.columns, globForms) {
		return classNamed
	}
	for _, f := range tokenForms {
		if builtinRules.matches(f) {
			return classNamed
		}
	}
	return classNone
}

func globsMatch(globs, names []string) bool {
	for _, g := range globs {
		for _, n := range names {
			if ok, _ := path.Match(g, n); ok {
				return true
			}
		}
	}
	return false
}

// Value masks one value of the named column. It reports the masked value
// and whether it changed.
func (m *Masker) Value(column string, v any) (any, bool) {
	return m.node([]string{column}, v, nil)
}

// Row returns a masked copy of one row.
func (m *Masker) Row(columns []string, row []any) []any {
	out := make([]any, len(row))
	for i, v := range row {
		if i < len(columns) {
			out[i], _ = m.Value(columns[i], v)
		} else {
			out[i] = v
		}
	}
	return out
}

// Rows masks rows in place and returns the names of the columns that had
// a masked value, in column order.
func (m *Masker) Rows(columns []string, rows [][]any) []string {
	hit := make([]bool, len(columns))
	for _, row := range rows {
		for i := range row {
			if i >= len(columns) {
				break
			}
			var changed bool
			row[i], changed = m.Value(columns[i], row[i])
			if changed {
				hit[i] = true
			}
		}
	}
	var out []string
	for i, h := range hit {
		if h {
			out = append(out, columns[i])
		}
	}
	return out
}

// Doc masks a values.Doc, map[string]any, or []any recursively and returns
// a masked copy plus the dotted paths of the masked fields (array indexes
// dropped), sorted and deduplicated. The input is not modified.
func (m *Masker) Doc(v any) (any, []string) {
	paths := map[string]bool{}
	out, _ := m.node(nil, v, paths)
	list := make([]string, 0, len(paths))
	for p := range paths {
		list = append(list, p)
	}
	sort.Strings(list)
	return out, list
}

// Text replaces every detected value in s with its marker.
func (m *Masker) Text(s string) string {
	out, _ := m.replaceSpans(s)
	return out
}

func (m *Masker) replaceSpans(s string) (string, bool) {
	spans := detect.Find(s)
	if len(spans) == 0 {
		return s, false
	}
	var b strings.Builder
	pos := 0
	changed := false
	for _, sp := range spans {
		if sp.Start < pos || sp.End > len(s) || sp.Start >= sp.End {
			continue
		}
		b.WriteString(s[pos:sp.Start])
		b.WriteString(m.marker(sp.Normal))
		pos = sp.End
		changed = true
	}
	if !changed {
		return s, false
	}
	b.WriteString(s[pos:])
	return b.String(), true
}

// node masks v found at the field path segs. paths, when non-nil,
// collects the dotted path of every masked leaf or container.
func (m *Masker) node(segs []string, v any, paths map[string]bool) (any, bool) {
	if v == nil {
		return nil, false
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
		return v, false
	}
	class := m.classify(segs)
	record := func() {
		if paths != nil {
			paths[strings.Join(segs, ".")] = true
		}
	}
	if class == classSpecial {
		record()
		return redactedMarker, true
	}
	switch c := v.(type) {
	case values.Doc:
		if class == classNamed {
			record()
			return m.marker(canonicalJSON(c)), true
		}
		out := make(values.Doc, len(c))
		changed := false
		for i, f := range c {
			nv, ch := m.node(append(segs[:len(segs):len(segs)], f.Key), f.Value, paths)
			out[i] = values.Field{Key: f.Key, Value: nv}
			changed = changed || ch
		}
		return out, changed
	case map[string]any:
		if class == classNamed {
			record()
			return m.marker(canonicalJSON(c)), true
		}
		out := make(map[string]any, len(c))
		changed := false
		for k, fv := range c {
			nv, ch := m.node(append(segs[:len(segs):len(segs)], k), fv, paths)
			out[k] = nv
			changed = changed || ch
		}
		return out, changed
	case []any:
		if class == classNamed {
			record()
			return m.marker(canonicalJSON(c)), true
		}
		out := make([]any, len(c))
		changed := false
		for i, ev := range c {
			nv, ch := m.node(segs, ev, paths)
			out[i] = nv
			changed = changed || ch
		}
		return out, changed
	}
	if class == classNamed {
		record()
		return m.marker(m.hashText(v)), true
	}
	switch c := v.(type) {
	case string:
		return m.maskString(segs, c, paths)
	default:
		if s, ok := integerString(v); ok && !tooShortForDetectors(s) {
			if _, normal, ok := detect.MatchNormal(s); ok {
				record()
				return m.marker(normal), true
			}
		}
	}
	return v, false
}

// tooShortForDetectors reports whether the decimal form of an integer
// has too few digits for any detector, so Match can be skipped.
func tooShortForDetectors(integer string) bool {
	return len(strings.TrimPrefix(integer, "-")) < detect.MinIntegerDigits
}

// maskString applies the value rules 5 to 7 to a string outside a name rule.
func (m *Masker) maskString(segs []string, s string, paths map[string]bool) (any, bool) {
	if _, normal, ok := detect.MatchNormal(s); ok {
		if paths != nil {
			paths[strings.Join(segs, ".")] = true
		}
		return m.marker(normal), true
	}
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		if decoded, err := values.DecodeOrdered([]byte(t)); err == nil {
			out, changed := m.node(segs, decoded, paths)
			if !changed {
				return s, false
			}
			return canonicalJSON(out), true
		}
	}
	out, changed := m.replaceSpans(s)
	if changed && paths != nil {
		paths[strings.Join(segs, ".")] = true
	}
	return out, changed
}

// hashText is the text hashed for a scalar masked by name: the detector's
// normal form when the value is recognized, otherwise its trimmed, NFC,
// case-folded string form.
func (m *Masker) hashText(v any) string {
	s := stringForm(v)
	if _, normal, ok := detect.MatchNormal(s); ok {
		return normal
	}
	return foldText(s)
}

// stringForm is the canonical string of a scalar.
func stringForm(v any) string {
	if s, ok := integerString(v); ok {
		return s
	}
	switch c := v.(type) {
	case string:
		return c
	case []byte:
		return string(c)
	case time.Time:
		return c.Format(time.RFC3339)
	case float32:
		return strconv.FormatFloat(float64(c), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(c, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// integerString returns the decimal form of an integer, including a float
// that holds a whole number (JSON numbers decode to float64).
func integerString(v any) (string, bool) {
	switch c := v.(type) {
	case int:
		return strconv.FormatInt(int64(c), 10), true
	case int8:
		return strconv.FormatInt(int64(c), 10), true
	case int16:
		return strconv.FormatInt(int64(c), 10), true
	case int32:
		return strconv.FormatInt(int64(c), 10), true
	case int64:
		return strconv.FormatInt(c, 10), true
	case uint:
		return strconv.FormatUint(uint64(c), 10), true
	case uint8:
		return strconv.FormatUint(uint64(c), 10), true
	case uint16:
		return strconv.FormatUint(uint64(c), 10), true
	case uint32:
		return strconv.FormatUint(uint64(c), 10), true
	case uint64:
		return strconv.FormatUint(c, 10), true
	case float64:
		if c == float64(int64(c)) && c > -1e15 && c < 1e15 {
			return strconv.FormatInt(int64(c), 10), true
		}
	}
	return "", false
}

// canonicalJSON encodes v compactly, without HTML escaping.
func canonicalJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
