package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// objectEncoder writes rows as JSON objects, one per line, whose keys
// follow the result's column order. Keys are encoded once up front; every
// value is encoded exactly as encoding/json encodes it, HTML characters
// (<, >, &) and U+2028/U+2029 escaped included.
type objectEncoder struct {
	keys [][]byte // `"column":` per column
	buf  bytes.Buffer
	enc  *json.Encoder // writes into buf
}

// newObjectEncoder refuses a result with a repeated column name, since a
// JSON object can hold each key only once and dropping a column silently
// would lose data.
func newObjectEncoder(columns []string) (*objectEncoder, error) {
	e := &objectEncoder{keys: make([][]byte, len(columns))}
	seen := make(map[string]bool, len(columns))
	for i, c := range columns {
		if seen[c] {
			return nil, fmt.Errorf("duplicate column name %q in result; give each column a unique alias", c)
		}
		seen[c] = true
		k, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		e.keys[i] = append(k, ':')
	}
	e.enc = json.NewEncoder(&e.buf)
	return e, nil
}

// encode writes row as one object and a newline to w; a value that cannot
// be encoded writes nothing.
func (e *objectEncoder) encode(w io.Writer, row []any) error {
	e.buf.Reset()
	e.buf.WriteByte('{')
	for i := 0; i < len(row) && i < len(e.keys); i++ {
		if i > 0 {
			e.buf.WriteByte(',')
		}
		e.buf.Write(e.keys[i])
		if err := e.value(row[i]); err != nil {
			return err
		}
	}
	e.buf.WriteString("}\n")
	_, err := w.Write(e.buf.Bytes())
	return err
}

// value appends one encoded value to buf: the common scalars directly,
// everything else through encoding/json, whose trailing newline is cut.
func (e *objectEncoder) value(v any) error {
	switch t := v.(type) {
	case nil:
		e.buf.WriteString("null")
	case bool:
		e.buf.Write(strconv.AppendBool(e.buf.AvailableBuffer(), t))
	case int64:
		e.buf.Write(strconv.AppendInt(e.buf.AvailableBuffer(), t, 10))
	default:
		if err := e.enc.Encode(v); err != nil {
			return err
		}
		e.buf.Truncate(e.buf.Len() - 1)
	}
	return nil
}
