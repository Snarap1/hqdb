package mongoshell

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// parseDoc mirrors internal/mongocmd's own parseDoc: it feeds translated
// text through the same bson.UnmarshalExtJSON the real runner uses, proving
// the translator's output parses into the expected BSON.
func parseDoc(t *testing.T, extJSON string) bson.D {
	t.Helper()
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(extJSON), false, &d); err != nil {
		t.Fatalf("UnmarshalExtJSON(%s): %v", extJSON, err)
	}
	return d
}

func TestTranslateObjects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bson.D
	}{
		{
			name: "unquoted keys and bare values",
			in:   `{name: 'Bob', age: 30, active: true}`,
			want: bson.D{{Key: "name", Value: "Bob"}, {Key: "age", Value: int32(30)}, {Key: "active", Value: true}},
		},
		{
			name: "double-quoted keys and strings still work",
			in:   `{"name": "Bob"}`,
			want: bson.D{{Key: "name", Value: "Bob"}},
		},
		{
			name: "trailing comma in object",
			in:   `{a: 1, b: 2,}`,
			want: bson.D{{Key: "a", Value: int32(1)}, {Key: "b", Value: int32(2)}},
		},
		{
			name: "nested document and array",
			in:   `{a: {b: [1, 2, 3,], c: 'x'},}`,
			want: bson.D{{Key: "a", Value: bson.D{
				{Key: "b", Value: bson.A{int32(1), int32(2), int32(3)}},
				{Key: "c", Value: "x"},
			}}},
		},
		{
			name: "string containing quotes, slashes, and parens",
			in:   `{note: 'it\'s a "test" / value (ok)'}`,
			want: bson.D{{Key: "note", Value: `it's a "test" / value (ok)`}},
		},
		{
			name: "null and false",
			in:   `{a: null, b: false}`,
			want: bson.D{{Key: "a", Value: nil}, {Key: "b", Value: false}},
		},
		{
			name: "float and negative numbers",
			in:   `{a: -1.5, b: 2.0e3}`,
			want: bson.D{{Key: "a", Value: -1.5}, {Key: "b", Value: 2000.0}},
		},
		{
			name: "dollar and underscore in unquoted key",
			in:   `{_id: 1, $set: 2}`,
			want: bson.D{{Key: "_id", Value: int32(1)}, {Key: "$set", Value: int32(2)}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Translate(c.in)
			if err != nil {
				t.Fatalf("Translate(%s): %v", c.in, err)
			}
			got := parseDoc(t, out)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Translate(%s) = %s\nparsed = %#v\nwant   = %#v", c.in, out, got, c.want)
			}
		})
	}
}

func TestTranslateObjectId(t *testing.T) {
	in := `{_id: ObjectId('507f191e810c19729de860ea')}`
	out, err := Translate(in)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	got := parseDoc(t, out)
	oid, err := bson.ObjectIDFromHex("507f191e810c19729de860ea")
	if err != nil {
		t.Fatalf("ObjectIDFromHex: %v", err)
	}
	want := bson.D{{Key: "_id", Value: oid}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestTranslateISODate(t *testing.T) {
	in := `{createdAt: ISODate('2020-01-02T03:04:05Z')}`
	out, err := Translate(in)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	got := parseDoc(t, out)
	if len(got) != 1 || got[0].Key != "createdAt" {
		t.Fatalf("got %#v", got)
	}
	if _, ok := got[0].Value.(bson.DateTime); !ok {
		t.Errorf("createdAt is %T, want bson.DateTime", got[0].Value)
	}
}

func TestTranslateNumberTypes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bson.D
	}{
		{
			name: "NumberLong quoted",
			in:   `{n: NumberLong('123456789012')}`,
			want: bson.D{{Key: "n", Value: int64(123456789012)}},
		},
		{
			name: "NumberLong bare",
			in:   `{n: NumberLong(42)}`,
			want: bson.D{{Key: "n", Value: int64(42)}},
		},
		{
			name: "NumberInt",
			in:   `{n: NumberInt(5)}`,
			want: bson.D{{Key: "n", Value: int32(5)}},
		},
		{
			name: "NumberDecimal",
			in:   `{n: NumberDecimal('19.99')}`,
			want: bson.D{{Key: "n", Value: mustDecimal(t, "19.99")}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Translate(c.in)
			if err != nil {
				t.Fatalf("Translate(%s): %v", c.in, err)
			}
			got := parseDoc(t, out)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Translate(%s) = %s\nparsed = %#v\nwant   = %#v", c.in, out, got, c.want)
			}
		})
	}
}

func mustDecimal(t *testing.T, s string) bson.Decimal128 {
	t.Helper()
	d, err := bson.ParseDecimal128(s)
	if err != nil {
		t.Fatalf("ParseDecimal128(%s): %v", s, err)
	}
	return d
}

func TestTranslateRegex(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		pattern string
		options string
	}{
		{name: "no flags", in: `{name: /^bob/}`, pattern: "^bob", options: ""},
		{name: "flags", in: `{name: /^bob/i}`, pattern: "^bob", options: "i"},
		{name: "escaped slash in pattern", in: `{path: /a\/b/}`, pattern: `a\/b`, options: ""},
		{name: "digit class", in: `{code: /\d+/gi}`, pattern: `\d+`, options: "gi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Translate(c.in)
			if err != nil {
				t.Fatalf("Translate(%s): %v", c.in, err)
			}
			got := parseDoc(t, out)
			if len(got) != 1 {
				t.Fatalf("got %#v", got)
			}
			re, ok := got[0].Value.(bson.Regex)
			if !ok {
				t.Fatalf("value is %T, want bson.Regex", got[0].Value)
			}
			if re.Pattern != c.pattern || re.Options != c.options {
				t.Errorf("got pattern=%q options=%q, want pattern=%q options=%q", re.Pattern, re.Options, c.pattern, c.options)
			}
		})
	}
}

func TestTranslateArrayTopLevel(t *testing.T) {
	// A pipeline is a bare array of stage documents.
	in := `[{$match: {status: 'active'}}, {$limit: 10},]`
	out, err := Translate(in)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	var wrapper struct {
		P []bson.D `bson:"p"`
	}
	if err := bson.UnmarshalExtJSON([]byte(`{"p":`+out+`}`), false, &wrapper); err != nil {
		t.Fatalf("UnmarshalExtJSON pipeline: %v", err)
	}
	want := []bson.D{
		{{Key: "$match", Value: bson.D{{Key: "status", Value: "active"}}}},
		{{Key: "$limit", Value: int32(10)}},
	}
	if !reflect.DeepEqual(wrapper.P, want) {
		t.Errorf("got %#v, want %#v", wrapper.P, want)
	}
}

func TestTranslateBlank(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t"} {
		out, err := Translate(in)
		if err != nil {
			t.Errorf("Translate(%q): %v", in, err)
		}
		if out != "" {
			t.Errorf("Translate(%q) = %q, want empty", in, out)
		}
	}
}

func TestTranslateErrors(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantPos int
	}{
		{name: "missing colon", in: `{a 1}`, wantPos: 3},
		{name: "unterminated object", in: `{a: 1`, wantPos: 5},
		{name: "unterminated array", in: `[1, 2`, wantPos: 5},
		{name: "unterminated string", in: `{a: 'x}`, wantPos: 4},
		{name: "unterminated regex", in: `{a: /x}`, wantPos: 4},
		{name: "unknown identifier", in: `{a: Frobnicate(1)}`, wantPos: 4},
		{name: "bad number", in: `{a: -}`, wantPos: 4},
		{name: "trailing garbage", in: `{a: 1} extra`, wantPos: 7},
		{name: "missing comma", in: `{a: 1 b: 2}`, wantPos: 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Translate(c.in)
			if err == nil {
				t.Fatalf("Translate(%s): want error, got nil", c.in)
			}
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Translate(%s): err is %T, want *ParseError", c.in, err)
			}
			if perr.Pos != c.wantPos {
				t.Errorf("Translate(%s): Pos = %d, want %d (err: %v)", c.in, perr.Pos, c.wantPos, err)
			}
		})
	}
}
