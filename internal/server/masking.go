package server

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/mask"
)

// maskKey is the session key for every Masker of this process (ADR-0005).
// Serve sets it once with mask.NewKey; it lives only in memory, so equal values share a
// marker within one MCP session and never across sessions.
var maskKey []byte

// maskerFor returns the Masker for a Profile, or nil when the Profile does
// not set mask = true. The Masker is built fresh per call from the freshly
// loaded [masking] table and the session key.
func maskerFor(cfg *config.Config, profile map[string]any) (*mask.Masker, error) {
	on, err := config.ProfileMask(profile)
	if err != nil || !on {
		return nil, err
	}
	return mask.New(cfg.Masking, maskKey)
}

// markerScreen refuses statement text that carries a masked marker, on a
// masked Profile (m != nil), whatever the Access mode: a marker is a
// placeholder, so it must not be written back or queried for.
func markerScreen(m *mask.Masker, texts ...string) error {
	if m == nil {
		return nil
	}
	for _, t := range texts {
		if v := mask.MarkerViolation(t); v != "" {
			return fmt.Errorf("Refused (masked profile): %s", v)
		}
	}
	return nil
}

// guard is the one screen every statement tool runs before executing: the
// marker screen first, then the access screen.
func guard(access config.Access, m *mask.Masker, violation string, texts ...string) error {
	if err := markerScreen(m, texts...); err != nil {
		return err
	}
	return screen(access, violation)
}

// maskErr passes a handler error through Text on a masked Profile, because
// database errors quote values (Key (email)=(ivan@x.ru) already exists).
// Every handler defers it right after openProfile.
func maskErr(m *mask.Masker, err error) error {
	if m == nil || err == nil {
		return err
	}
	return errors.New(m.Text(err.Error()))
}

// maskRedis masks one run_redis result. Key listings (KEYS, SCAN,
// RANDOMKEY) go through Text, so a key such as user:ivan@x.ru is masked
// without hashing the whole key; every other reply goes through Doc, which
// masks hash replies by field name and bare strings by the value rules
// with an empty column name.
func maskRedis(m *mask.Masker, command string, result any) any {
	if m == nil {
		return result
	}
	switch strings.ToLower(firstField(command)) {
	case "keys", "scan", "randomkey":
		return textAll(m, result)
	}
	out, _ := m.Doc(result)
	return out
}

func firstField(command string) string {
	f := strings.Fields(command)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// textAll applies Text to every string inside a JSON-safe value.
func textAll(m *mask.Masker, v any) any {
	switch t := v.(type) {
	case string:
		return m.Text(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = textAll(m, e)
		}
		return out
	}
	return v
}

// maskMongo masks the Documents of a run_mongo result and returns the
// masked field paths. distinct returns bare values, masked by the field's
// own name.
func maskMongo(m *mask.Masker, op, field string, docs []any) ([]any, []string) {
	if m == nil {
		return docs, nil
	}
	if op == "distinct" {
		changedAny := false
		out := make([]any, len(docs))
		for i, v := range docs {
			var changed bool
			out[i], changed = m.Value(field, v)
			changedAny = changedAny || changed
		}
		if changedAny {
			return out, []string{field}
		}
		return out, nil
	}
	seen := map[string]bool{}
	out := make([]any, len(docs))
	for i, d := range docs {
		var paths []string
		out[i], paths = m.Doc(d)
		for _, p := range paths {
			seen[p] = true
		}
	}
	fields := make([]string, 0, len(seen))
	for p := range seen {
		fields = append(fields, p)
	}
	sort.Strings(fields)
	return out, fields
}
