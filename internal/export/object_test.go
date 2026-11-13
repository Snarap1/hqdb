package export

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/Snarap1/hqdb/internal/fakesql"
)

func TestObjectEncoderKeepsColumnOrder(t *testing.T) {
	cases := []struct {
		columns []string
		row     []any
		want    string
	}{
		{[]string{"z", "a", "m"}, []any{int64(1), "x", nil}, `{"z":1,"a":"x","m":null}` + "\n"},
		{[]string{"b", "B", "a"}, []any{true, false, 1.5}, `{"b":true,"B":false,"a":1.5}` + "\n"},
		{[]string{"<k>", "ключ", `q"uote`}, []any{"v", "в", int64(-3)}, `{"\u003ck\u003e":"v","ключ":"в","q\"uote":-3}` + "\n"},
		{[]string{"only"}, []any{"x", "extra value is dropped"}, `{"only":"x"}` + "\n"},
		{[]string{"a", "missing"}, []any{int64(1)}, `{"a":1}` + "\n"},
		{nil, nil, "{}\n"},
	}
	for _, c := range cases {
		e, err := newObjectEncoder(c.columns)
		if err != nil {
			t.Fatalf("%v: %v", c.columns, err)
		}
		var out bytes.Buffer
		if err := e.encode(&out, c.row); err != nil {
			t.Fatalf("%v: %v", c.columns, err)
		}
		if out.String() != c.want {
			t.Errorf("%v:\ngot  %q\nwant %q", c.columns, out.String(), c.want)
		}
	}
}

func TestObjectEncoderRefusesDuplicateColumns(t *testing.T) {
	cases := []struct {
		columns []string
		want    string
	}{
		{[]string{"id", "id"}, `duplicate column name "id" in result; give each column a unique alias`},
		{[]string{"id", "name", "x", "name"}, `duplicate column name "name" in result; give each column a unique alias`},
		{[]string{"", ""}, `duplicate column name "" in result; give each column a unique alias`},
	}
	for _, c := range cases {
		_, err := newObjectEncoder(c.columns)
		if err == nil || err.Error() != c.want {
			t.Errorf("%v: err = %v, want %q", c.columns, err, c.want)
		}
	}
	if _, err := newObjectEncoder([]string{"id", "ID"}); err != nil {
		t.Errorf("names differing in case are distinct JSON keys: %v", err)
	}
}

// TestObjectEncoderValuesMatchEncodingJSON pins every value to the bytes
// json.Marshal gives it, HTML and line-separator escaping included.
func TestObjectEncoderValuesMatchEncodingJSON(t *testing.T) {
	cases := []any{
		nil, true, false,
		int64(0), int64(-1), int64(math.MaxInt64), int64(math.MinInt64),
		0.0, math.Copysign(0, -1), 1.5, -3.25, 1e20, 1e21, 1e-6, 1e-7, 123456789.125, math.MaxFloat64, math.SmallestNonzeroFloat64,
		"", "plain", "<b>&amp;</b>", `quote " and \ backslash`, "line\u2028sep\u2029", "tab\tnew\nline\r", "\x00\x1f", "bad utf8 \xff\xfe", "unicode ñ 漢字 😀",
		[]any{"a", int64(1), nil, []any{true}},
		map[string]any{"z": int64(1), "a": "<x>", "m": map[string]any{"k": 2.5}},
		int(7), float32(0.1), []byte("raw bytes"),
	}
	for _, v := range cases {
		e, err := newObjectEncoder([]string{"v"})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := e.encode(&out, []any{v}); err != nil {
			t.Fatalf("%#v: %v", v, err)
		}
		enc, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"v":` + string(enc) + "}\n"; out.String() != want {
			t.Errorf("%#v:\ngot  %q\nwant %q", v, out.String(), want)
		}
	}
}

func TestObjectEncoderReportsUnsupportedValueAndWritesNothing(t *testing.T) {
	e, err := newObjectEncoder([]string{"ok", "bad"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = e.encode(&out, []any{int64(1), math.NaN()})
	_, want := json.Marshal(math.NaN())
	if err == nil || err.Error() != want.Error() {
		t.Errorf("err = %v, want %v", err, want)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q for a row that failed", out.String())
	}
}

func TestQueryRefusesDuplicateColumnsBeforeWriting(t *testing.T) {
	db, _ := fakesql.OpenColumns(5, "id", "name", "id")
	t.Cleanup(func() { db.Close() })
	dir := t.TempDir()
	for _, format := range []string{"json", "ndjson"} {
		dest := filepath.Join(dir, "out."+format)
		n, err := Query(context.Background(), db, "SELECT a.id, a.name, b.id", dest, format, 0, nil)
		want := `duplicate column name "id" in result; give each column a unique alias`
		if err == nil || err.Error() != want || n != 0 {
			t.Errorf("%s: n = %d, err = %v; want 0 and %q", format, n, err, want)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Errorf("%s: the refused export created %s", format, dest)
		}
	}
	dest := filepath.Join(dir, "out.csv")
	if n, err := Query(context.Background(), db, "SELECT a.id, a.name, b.id", dest, "csv", 0, nil); err != nil || n != 5 {
		t.Errorf("csv keeps duplicate header names: n = %d, err = %v", n, err)
	}
	if got, _ := os.ReadFile(dest); !bytes.HasPrefix(got, []byte("id,name,id\n")) {
		t.Errorf("csv header = %q, want id,name,id", got)
	}
}
