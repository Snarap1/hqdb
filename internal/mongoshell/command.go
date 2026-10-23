package mongoshell

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Snarap1/hqdb/internal/mongocmd"
)

// Supported names the mongosh subset ParseCommand accepts; every "not
// supported" error ends with it.
const Supported = "db.<coll>.find(filter, projection) with .sort(...), .limit(...), .skip(...); " +
	"db.<coll>.aggregate([...]); db.<coll>.countDocuments(filter); " +
	"db.<coll>.distinct('field', filter); use <db>"

// Command is one parsed mongosh statement: either a Database switch (Use
// is set) or a Request for mongocmd.Run. Request.Database is left empty
// for the caller (the Console's current Database) to fill.
type Command struct {
	Use     string
	Request mongocmd.Request
}

func unsupported(what string) error {
	return fmt.Errorf("%s is not supported; supported: %s", what, Supported)
}

// ParseCommand parses one mongosh statement of the supported subset into a
// Command. Filter, projection, sort, and pipeline arguments are mongo shell
// literals, translated to compact Extended JSON (key order kept). It never
// screens what it parses: an aggregate with a writing stage parses fine
// (ADR-0003), and the caller decides what to do with it.
func ParseCommand(src string) (Command, error) {
	s := strings.TrimSpace(src)
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if s == "" {
		return Command{}, errors.New("empty statement")
	}
	if f := strings.Fields(s); f[0] == "use" {
		if len(f) != 2 {
			return Command{}, errors.New("use takes a database name: use <db>")
		}
		return Command{Use: f[1]}, nil
	}
	if !strings.HasPrefix(s, "db.") {
		return Command{}, unsupported(fmt.Sprintf("%q", firstWord(s)))
	}
	coll, rest, err := collection(s[len("db."):])
	if err != nil {
		return Command{}, err
	}
	calls, err := splitCalls(rest)
	if err != nil {
		return Command{}, err
	}
	if len(calls) == 0 {
		return Command{}, unsupported(fmt.Sprintf("%q without a method", s))
	}
	req := mongocmd.Request{Collection: coll}
	head := calls[0]
	args, err := head.values()
	if err != nil {
		return Command{}, err
	}
	switch head.name {
	case "find":
		req.Operation = "find"
		if len(args) > 2 {
			return Command{}, errors.New("find takes at most 2 arguments: find(filter, projection)")
		}
		if len(args) > 0 {
			if req.Filter, err = document("filter", args[0]); err != nil {
				return Command{}, err
			}
		}
		if len(args) > 1 {
			if req.Projection, err = document("projection", args[1]); err != nil {
				return Command{}, err
			}
		}
	case "aggregate":
		req.Operation = "aggregate"
		if len(args) != 1 || !bytes.HasPrefix(args[0], []byte("[")) {
			return Command{}, errors.New("aggregate takes one pipeline array: aggregate([...])")
		}
		req.Pipeline = string(args[0])
	case "countDocuments":
		req.Operation = "count"
		if len(args) > 1 {
			return Command{}, errors.New("countDocuments takes at most 1 argument: countDocuments(filter)")
		}
		if len(args) == 1 {
			if req.Filter, err = document("filter", args[0]); err != nil {
				return Command{}, err
			}
		}
	case "distinct":
		req.Operation = "distinct"
		if len(args) < 1 || len(args) > 2 || json.Unmarshal(args[0], &req.Field) != nil || req.Field == "" {
			return Command{}, errors.New("distinct takes a field name and an optional filter: distinct('field', filter)")
		}
		if len(args) == 2 {
			if req.Filter, err = document("filter", args[1]); err != nil {
				return Command{}, err
			}
		}
	default:
		return Command{}, unsupported(fmt.Sprintf("%q", head.name))
	}
	for _, c := range calls[1:] {
		if req.Operation != "find" {
			return Command{}, fmt.Errorf("\".%s()\" is not supported after %s; supported: %s", c.name, head.name, Supported)
		}
		if err := modifier(&req, c); err != nil {
			return Command{}, err
		}
	}
	return Command{Request: req}, nil
}

func modifier(req *mongocmd.Request, c call) error {
	args, err := c.values()
	if err != nil {
		return err
	}
	switch c.name {
	case "sort":
		if len(args) != 1 {
			return errors.New("sort must be a document: sort({field: 1})")
		}
		req.Sort, err = document("sort", args[0])
		return err
	case "limit", "skip":
		var n int
		if len(args) != 1 || json.Unmarshal(args[0], &n) != nil || n < 0 {
			return fmt.Errorf("%s takes a non-negative integer", c.name)
		}
		if c.name == "limit" {
			req.Limit = n
		} else {
			req.Skip = n
		}
		return nil
	}
	return unsupported(fmt.Sprintf("%q", "."+c.name+"()"))
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

func document(what string, raw json.RawMessage) (string, error) {
	if !bytes.HasPrefix(raw, []byte("{")) {
		return "", fmt.Errorf("%s must be a document", what)
	}
	return string(raw), nil
}

// collection reads the Collection off s (the text after "db."): either
// getCollection('name') or the dotted name up to the last "." before the
// method's "(". rest starts at that "." of the method call.
func collection(s string) (coll, rest string, err error) {
	if strings.HasPrefix(s, "getCollection(") {
		calls, err := splitCalls("." + s)
		if err != nil {
			return "", "", err
		}
		args, err := calls[0].values()
		if err != nil {
			return "", "", err
		}
		if len(args) != 1 || json.Unmarshal(args[0], &coll) != nil || coll == "" {
			return "", "", errors.New("getCollection takes a collection name: db.getCollection('name')")
		}
		return coll, s[calls[0].end-1:], nil
	}
	paren := strings.IndexByte(s, '(')
	if paren < 0 {
		return "", "", unsupported(fmt.Sprintf("%q without a method", "db."+s))
	}
	dot := strings.LastIndexByte(s[:paren], '.')
	if dot <= 0 {
		return "", "", unsupported(fmt.Sprintf("%q", "db."+s[:paren]))
	}
	return strings.TrimSpace(s[:dot]), s[dot:], nil
}

// call is one ".name(args)" link of a chain; end is the byte offset just
// past its ")" in the text splitCalls was given.
type call struct {
	name string
	args string
	end  int
}

// values translates the call's arguments from mongo shell literals into
// one compact Extended JSON value each.
func (c call) values() ([]json.RawMessage, error) {
	translated, err := Translate("[" + c.args + "]")
	if err != nil {
		var pe *ParseError
		if errors.As(err, &pe) {
			// Shift the position past the "[" we added.
			return nil, fmt.Errorf("%s(): %s (at position %d of its arguments)", c.name, pe.Msg, max(0, pe.Pos-1))
		}
		return nil, err
	}
	var raws []json.RawMessage
	if err := json.Unmarshal([]byte(translated), &raws); err != nil {
		return nil, err
	}
	for i, r := range raws {
		var buf bytes.Buffer
		if err := json.Compact(&buf, r); err != nil {
			return nil, err
		}
		raws[i] = buf.Bytes()
	}
	return raws, nil
}

// splitCalls splits a chain ".a(x).b(y)" into its calls, matching
// parentheses outside strings and regex literals.
func splitCalls(s string) ([]call, error) {
	var calls []call
	i := 0
	for {
		for i < len(s) && isSpace(rune(s[i])) {
			i++
		}
		if i == len(s) {
			return calls, nil
		}
		if s[i] != '.' {
			return nil, fmt.Errorf("unexpected %q after the method call", s[i:])
		}
		open := strings.IndexByte(s[i:], '(')
		if open < 0 {
			return nil, unsupported(fmt.Sprintf("%q", s[i:]))
		}
		open += i
		name := strings.TrimSpace(s[i+1 : open])
		closing, err := matchParen(s, open)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call{name: name, args: s[open+1 : closing], end: closing + 1})
		i = closing + 1
	}
}

// matchParen returns the index of the ")" closing the "(" at s[open].
func matchParen(s string, open int) (int, error) {
	depth := 0
	prev := byte('(') // last non-space code byte, to tell a regex "/" from division
	for i := open; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		case c == '/' && strings.IndexByte("([{,:", prev) >= 0:
			j := i + 1
			for j < len(s) && s[j] != '/' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
			if depth == 0 {
				if c != ')' {
					return 0, errors.New("unbalanced brackets in the method call")
				}
				return i, nil
			}
		}
		if !isSpace(rune(c)) {
			prev = c
		}
	}
	return 0, errors.New("unbalanced parentheses: missing \")\"")
}
