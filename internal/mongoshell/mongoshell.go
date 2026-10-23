// Package mongoshell translates mongo shell literal syntax (the relaxed,
// JavaScript-flavored object/array literals used by mongosh and Compass)
// into the Extended JSON text that internal/mongocmd's document and
// pipeline parsers accept.
//
// It has no Bubble Tea dependency: Translate is a pure text-to-text
// translator the terminal UI's mongodb Data view calls before handing a
// Filter or Sort string to the existing mongodb runner, and ParseCommand
// turns a mongodb Console statement (the supported mongosh subset) into a
// mongocmd.Request. The MCP
// run_mongo tool is unaffected and keeps accepting strict Extended JSON
// only.
package mongoshell

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ParseError is returned when src cannot be translated. Pos is the
// character (rune) offset into src of the offending character.
type ParseError struct {
	Pos int
	Msg string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s (at position %d)", e.Msg, e.Pos)
}

// Translate turns a mongo shell literal (an object, an array of stage
// documents, or any other JSON-like value) into Extended JSON text that
// bson.UnmarshalExtJSON accepts.
//
// It covers unquoted object keys, single-quoted strings, trailing commas,
// ObjectId('...'), ISODate('...'), NumberLong(...), NumberInt(...),
// NumberDecimal(...), and /regex/flags literals, nested to any depth. A
// blank or whitespace-only src translates to "" so callers can treat it the
// same way mongocmd treats an empty filter.
func Translate(src string) (string, error) {
	if strings.TrimSpace(src) == "" {
		return "", nil
	}
	t := &translator{runes: []rune(src)}
	var buf strings.Builder
	t.skipSpace()
	if err := t.value(&buf); err != nil {
		return "", err
	}
	t.skipSpace()
	if !t.atEnd() {
		return "", t.errorf("unexpected trailing input %q", string(t.cur()))
	}
	return buf.String(), nil
}

type translator struct {
	runes []rune
	pos   int
}

func (t *translator) atEnd() bool { return t.pos >= len(t.runes) }

func (t *translator) cur() rune { return t.runes[t.pos] }

func (t *translator) skipSpace() {
	for !t.atEnd() && isSpace(t.cur()) {
		t.pos++
	}
}

func (t *translator) errorAt(pos int, format string, args ...any) error {
	return &ParseError{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

func (t *translator) errorf(format string, args ...any) error {
	return t.errorAt(t.pos, format, args...)
}

// value parses one JSON-like value at the current position and writes its
// Extended JSON translation to buf.
func (t *translator) value(buf *strings.Builder) error {
	t.skipSpace()
	if t.atEnd() {
		return t.errorf("unexpected end of input")
	}
	switch r := t.cur(); {
	case r == '{':
		return t.object(buf)
	case r == '[':
		return t.array(buf)
	case r == '\'' || r == '"':
		s, err := t.quotedString()
		if err != nil {
			return err
		}
		writeJSONString(buf, s)
		return nil
	case r == '/':
		return t.regex(buf)
	case r == '-' || isDigit(r):
		return t.number(buf)
	case isIdentStart(r):
		return t.identifierValue(buf)
	default:
		return t.errorf("unexpected character %q", string(r))
	}
}

func (t *translator) object(buf *strings.Builder) error {
	t.pos++ // consume '{'
	buf.WriteByte('{')
	first := true
	for {
		t.skipSpace()
		if t.atEnd() {
			return t.errorf("unterminated object")
		}
		if t.cur() == '}' {
			t.pos++
			break
		}
		if !first {
			buf.WriteString(", ")
		}
		key, err := t.key()
		if err != nil {
			return err
		}
		t.skipSpace()
		if t.atEnd() || t.cur() != ':' {
			return t.errorf("expected ':' after object key")
		}
		t.pos++
		writeJSONString(buf, key)
		buf.WriteString(": ")
		t.skipSpace()
		if err := t.value(buf); err != nil {
			return err
		}
		first = false
		t.skipSpace()
		if t.atEnd() {
			return t.errorf("unterminated object")
		}
		switch t.cur() {
		case ',':
			t.pos++
		case '}':
			t.pos++
			buf.WriteByte('}')
			return nil
		default:
			return t.errorf("expected ',' or '}' in object")
		}
	}
	buf.WriteByte('}')
	return nil
}

func (t *translator) array(buf *strings.Builder) error {
	t.pos++ // consume '['
	buf.WriteByte('[')
	first := true
	for {
		t.skipSpace()
		if t.atEnd() {
			return t.errorf("unterminated array")
		}
		if t.cur() == ']' {
			t.pos++
			break
		}
		if !first {
			buf.WriteString(", ")
		}
		if err := t.value(buf); err != nil {
			return err
		}
		first = false
		t.skipSpace()
		if t.atEnd() {
			return t.errorf("unterminated array")
		}
		switch t.cur() {
		case ',':
			t.pos++
		case ']':
			t.pos++
			buf.WriteByte(']')
			return nil
		default:
			return t.errorf("expected ',' or ']' in array")
		}
	}
	buf.WriteByte(']')
	return nil
}

// key parses an object key: a quoted string or a bare, unquoted identifier.
func (t *translator) key() (string, error) {
	t.skipSpace()
	if t.atEnd() {
		return "", t.errorf("expected object key")
	}
	if r := t.cur(); r == '\'' || r == '"' {
		return t.quotedString()
	}
	if isIdentStart(t.cur()) {
		start := t.pos
		for !t.atEnd() && isIdentPart(t.cur()) {
			t.pos++
		}
		return string(t.runes[start:t.pos]), nil
	}
	return "", t.errorf("expected object key")
}

// quotedString parses a single- or double-quoted string, resolving the
// usual backslash escapes, and returns its decoded value.
func (t *translator) quotedString() (string, error) {
	start := t.pos
	quote := t.cur()
	t.pos++
	var sb strings.Builder
	for {
		if t.atEnd() {
			return "", t.errorAt(start, "unterminated string")
		}
		r := t.cur()
		if r == '\\' {
			t.pos++
			if t.atEnd() {
				return "", t.errorAt(start, "unterminated string")
			}
			esc := t.cur()
			switch esc {
			case 'n':
				sb.WriteByte('\n')
				t.pos++
			case 't':
				sb.WriteByte('\t')
				t.pos++
			case 'r':
				sb.WriteByte('\r')
				t.pos++
			case 'b':
				sb.WriteByte('\b')
				t.pos++
			case 'f':
				sb.WriteByte('\f')
				t.pos++
			case 'u':
				t.pos++
				if t.pos+4 > len(t.runes) {
					return "", t.errorAt(start, "invalid unicode escape")
				}
				hex := string(t.runes[t.pos : t.pos+4])
				n, err := strconv.ParseUint(hex, 16, 32)
				if err != nil {
					return "", t.errorAt(start, "invalid unicode escape")
				}
				sb.WriteRune(rune(n))
				t.pos += 4
			default:
				// \', \", \\, \/, and anything else: keep the escaped
				// character literally.
				sb.WriteRune(esc)
				t.pos++
			}
			continue
		}
		if r == quote {
			t.pos++
			return sb.String(), nil
		}
		sb.WriteRune(r)
		t.pos++
	}
}

// regex parses a /pattern/flags literal and emits an Extended JSON
// $regularExpression.
func (t *translator) regex(buf *strings.Builder) error {
	start := t.pos
	t.pos++ // consume '/'
	var pattern strings.Builder
	for {
		if t.atEnd() {
			return t.errorAt(start, "unterminated regex literal")
		}
		r := t.cur()
		if r == '\n' {
			return t.errorAt(start, "unterminated regex literal")
		}
		if r == '\\' {
			pattern.WriteRune(r)
			t.pos++
			if t.atEnd() {
				return t.errorAt(start, "unterminated regex literal")
			}
			pattern.WriteRune(t.cur())
			t.pos++
			continue
		}
		if r == '/' {
			t.pos++
			break
		}
		pattern.WriteRune(r)
		t.pos++
	}
	flagsStart := t.pos
	for !t.atEnd() && unicode.IsLetter(t.cur()) {
		t.pos++
	}
	flags := string(t.runes[flagsStart:t.pos])
	buf.WriteString(`{"$regularExpression": {"pattern": `)
	writeJSONString(buf, pattern.String())
	buf.WriteString(`, "options": `)
	writeJSONString(buf, flags)
	buf.WriteString(`}}`)
	return nil
}

// number parses a JSON number literal and writes it to buf unchanged.
func (t *translator) number(buf *strings.Builder) error {
	s, err := t.scanNumberText()
	if err != nil {
		return err
	}
	buf.WriteString(s)
	return nil
}

func (t *translator) scanNumberText() (string, error) {
	start := t.pos
	if !t.atEnd() && t.cur() == '-' {
		t.pos++
	}
	if t.atEnd() || !isDigit(t.cur()) {
		return "", t.errorAt(start, "invalid number")
	}
	for !t.atEnd() && isDigit(t.cur()) {
		t.pos++
	}
	if !t.atEnd() && t.cur() == '.' {
		t.pos++
		if t.atEnd() || !isDigit(t.cur()) {
			return "", t.errorAt(start, "invalid number")
		}
		for !t.atEnd() && isDigit(t.cur()) {
			t.pos++
		}
	}
	if !t.atEnd() && (t.cur() == 'e' || t.cur() == 'E') {
		t.pos++
		if !t.atEnd() && (t.cur() == '+' || t.cur() == '-') {
			t.pos++
		}
		if t.atEnd() || !isDigit(t.cur()) {
			return "", t.errorAt(start, "invalid number")
		}
		for !t.atEnd() && isDigit(t.cur()) {
			t.pos++
		}
	}
	return string(t.runes[start:t.pos]), nil
}

// bsonCtors maps a constructor identifier to the Extended JSON key its
// single argument fills in.
var bsonCtors = map[string]string{
	"ObjectId":      "$oid",
	"ISODate":       "$date",
	"NumberLong":    "$numberLong",
	"NumberInt":     "$numberInt",
	"NumberDecimal": "$numberDecimal",
}

// identifierValue parses a bare word used as a value: true, false, null, or
// one of the bsonCtors constructor calls.
func (t *translator) identifierValue(buf *strings.Builder) error {
	start := t.pos
	for !t.atEnd() && isIdentPart(t.cur()) {
		t.pos++
	}
	word := string(t.runes[start:t.pos])
	switch word {
	case "true", "false", "null":
		buf.WriteString(word)
		return nil
	}
	if key, ok := bsonCtors[word]; ok {
		return t.constructor(buf, word, key)
	}
	return t.errorAt(start, "unsupported identifier %q", word)
}

// constructor parses the "(arg)" following a bsonCtors name and emits
// {"<key>": "<arg>"}.
func (t *translator) constructor(buf *strings.Builder, name, key string) error {
	t.skipSpace()
	if t.atEnd() || t.cur() != '(' {
		return t.errorf("expected '(' after %s", name)
	}
	t.pos++
	t.skipSpace()
	if t.atEnd() {
		return t.errorf("unterminated %s(...)", name)
	}
	var arg string
	switch {
	case t.cur() == '\'' || t.cur() == '"':
		s, err := t.quotedString()
		if err != nil {
			return err
		}
		arg = s
	case t.cur() == '-' || isDigit(t.cur()):
		s, err := t.scanNumberText()
		if err != nil {
			return err
		}
		arg = s
	default:
		return t.errorf("expected string or number argument to %s", name)
	}
	t.skipSpace()
	if t.atEnd() || t.cur() != ')' {
		return t.errorf("expected ')' after %s argument", name)
	}
	t.pos++
	buf.WriteString(`{"`)
	buf.WriteString(key)
	buf.WriteString(`": `)
	writeJSONString(buf, arg)
	buf.WriteString(`}`)
	return nil
}

// writeJSONString appends s to buf as a quoted, escaped JSON string.
func writeJSONString(buf *strings.Builder, s string) {
	b, err := json.Marshal(s)
	if err != nil {
		// s is always a valid Go string; Marshal of a string never fails.
		panic(err)
	}
	buf.Write(b)
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isIdentStart(r rune) bool { return unicode.IsLetter(r) || r == '_' || r == '$' }

func isIdentPart(r rune) bool { return isIdentStart(r) || unicode.IsDigit(r) }
