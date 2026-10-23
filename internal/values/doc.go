package values

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Field is one key/value pair of a Doc.
type Field struct {
	Key   string
	Value any
}

// Doc is a JSON object that keeps its field order, such as a mongodb
// Document as the server stored it. It marshals to JSON in that order.
// Values are JSON-safe: nil, bool, float64, string, []any, or Doc.
type Doc []Field

// Get returns the value of the field named key.
func (d Doc) Get(key string) (any, bool) {
	for _, f := range d {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

// MarshalJSON writes the fields in order.
func (d Doc) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range d {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(f.Key)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		v, err := json.Marshal(f.Value)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// DecodeOrdered decodes one JSON value like json.Unmarshal into an any,
// except that objects become Doc with their field order kept.
func DecodeOrdered(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("unexpected data after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			d := Doc{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				d = append(d, Field{Key: k, Value: v})
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return d, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return a, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	default:
		return t, nil
	}
}
