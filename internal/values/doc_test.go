package values

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDecodeOrderedKeepsFieldOrder(t *testing.T) {
	got, err := DecodeOrdered([]byte(`{"z":1,"_id":{"$oid":"65a1"},"a":[true,null,{"y":"s","b":2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Doc{
		{Key: "z", Value: float64(1)},
		{Key: "_id", Value: Doc{{Key: "$oid", Value: "65a1"}}},
		{Key: "a", Value: []any{true, nil, Doc{{Key: "y", Value: "s"}, {Key: "b", Value: float64(2)}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DecodeOrdered = %#v, want %#v", got, want)
	}
}

func TestDecodeOrderedRejectsTrailingData(t *testing.T) {
	if _, err := DecodeOrdered([]byte(`{"a":1} 2`)); err == nil {
		t.Error("trailing data must fail")
	}
}

func TestDocMarshalsInFieldOrder(t *testing.T) {
	d := Doc{{Key: "z", Value: 1}, {Key: "a", Value: Doc{{Key: "q", Value: "x"}, {Key: "b", Value: nil}}}}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"z":1,"a":{"q":"x","b":null}}`; string(b) != want {
		t.Errorf("Marshal = %s, want %s", b, want)
	}
	if b, _ := json.Marshal(Doc{}); string(b) != `{}` {
		t.Errorf("empty Doc = %s, want {}", b)
	}
}

func TestDocGet(t *testing.T) {
	d := Doc{{Key: "a", Value: 1}}
	if v, ok := d.Get("a"); !ok || v != 1 {
		t.Errorf("Get(a) = %v, %v", v, ok)
	}
	if _, ok := d.Get("b"); ok {
		t.Error("Get(b) found a missing field")
	}
}
