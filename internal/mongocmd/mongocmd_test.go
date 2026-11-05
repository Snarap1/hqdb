package mongocmd

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Snarap1/hqdb/internal/values"
)

func TestNormalizeOperation(t *testing.T) {
	cases := map[string]string{
		"":            "find",
		" Find ":      "find",
		"aggregate":   "aggregate",
		"insertOne":   "insertOne",
		"INSERTMANY":  "insertMany",
		" updateone ": "updateOne",
		"deleteone":   "deleteOne",
		"replaceOne":  "replaceone",
	}
	for op, want := range cases {
		if got := NormalizeOperation(op); got != want {
			t.Errorf("NormalizeOperation(%q) = %q, want %q", op, got, want)
		}
	}
}

func TestOperationViolation(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"find":       "",
		"aggregate":  "",
		"count":      "",
		"distinct":   "",
		"insertOne":  `operation "insertOne" writes data; this server permits reads only`,
		"insertmany": `operation "insertMany" writes data; this server permits reads only`,
		" UPDATEONE": `operation "updateOne" writes data; this server permits reads only`,
		"deleteOne":  `operation "deleteOne" writes data; this server permits reads only`,
	}
	for op, want := range cases {
		if got := OperationViolation(op); got != want {
			t.Errorf("OperationViolation(%q) = %q, want %q", op, got, want)
		}
	}
}

// Invalid write arguments fail before anything reaches a collection, so
// a nil collection is enough here.
func TestWriteRejectsInvalidArguments(t *testing.T) {
	cases := []struct {
		name    string
		req     Request
		wantErr string
	}{
		{"insertOne without document", Request{Operation: "insertOne"}, "insertOne requires document"},
		{"insertOne not an object", Request{Operation: "insertOne", Document: "1"}, "document must be a JSON object"},
		{"insertMany without documents", Request{Operation: "insertMany"}, "insertMany requires documents"},
		{"insertMany not an array", Request{Operation: "insertMany", Documents: `{"a": 1}`}, "documents must be a JSON array of objects"},
		{"insertMany empty", Request{Operation: "insertMany", Documents: "[]"}, "insertMany requires at least one document"},
		{"updateOne without filter", Request{Operation: "updateOne", Update: `{"$set": {"a": 1}}`}, "updateOne requires filter; pass {} to match any document"},
		{"updateOne without update", Request{Operation: "updateOne", Filter: "{}"}, "updateOne requires update"},
		{"updateOne bad pipeline", Request{Operation: "updateOne", Filter: "{}", Update: "[1]"}, "pipeline must be a JSON array of stage objects"},
		{"deleteOne without filter", Request{Operation: "deleteOne"}, "deleteOne requires filter; pass {} to match any document"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := write(context.Background(), nil, c.req.Operation, nil, c.req, JSON)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("write error = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestParseUpdate(t *testing.T) {
	doc, err := parseUpdate(`{"$set": {"a": 1}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.(bson.D); !ok {
		t.Errorf("update document parsed as %T, want bson.D", doc)
	}
	pipeline, err := parseUpdate(` [{"$set": {"a": 1}}]`)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := pipeline.(bson.A); !ok || len(p) != 1 {
		t.Errorf("update pipeline parsed as %#v, want one stage", pipeline)
	}
}

func TestOrderedJSONKeepsServerFieldOrder(t *testing.T) {
	oid, _ := bson.ObjectIDFromHex("65a1b2c3d4e5f60718293a4b")
	got, err := OrderedJSON(bson.D{{Key: "z", Value: int32(3)}, {Key: "_id", Value: oid}, {Key: "a", Value: bson.D{{Key: "y", Value: "s"}, {Key: "b", Value: bson.A{"x"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := values.Doc{
		{Key: "z", Value: float64(3)},
		{Key: "_id", Value: values.Doc{{Key: "$oid", Value: "65a1b2c3d4e5f60718293a4b"}}},
		{Key: "a", Value: values.Doc{{Key: "y", Value: "s"}, {Key: "b", Value: []any{"x"}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OrderedJSON = %#v, want %#v", got, want)
	}
}

func TestPipelineViolation(t *testing.T) {
	allowed := []string{
		`[{"$match": {"status": "active"}}, {"$group": {"_id": "$kind", "n": {"$sum": 1}}}]`,
		`[{"$lookup": {"from": "b", "pipeline": [{"$match": {}}], "as": "bs"}}]`,
		`[{"$match": {"note": "$out"}}]`,
		`[]`,
	}
	for _, p := range allowed {
		pipeline, err := parsePipeline(p)
		if err != nil {
			t.Fatalf("parsePipeline(%s): %v", p, err)
		}
		if v := PipelineViolation(pipeline); v != "" {
			t.Errorf("PipelineViolation(%s) = %q, want allowed", p, v)
		}
	}
	refused := map[string]string{
		`[{"$match": {}}, {"$out": "copy"}]`:                                       "$out",
		`[{"$merge": {"into": "copy"}}]`:                                           "$merge",
		`[{"$facet": {"a": [{"$match": {}}], "b": [{"$out": "x"}]}}]`:              "$out",
		`[{"$unionWith": {"coll": "b", "pipeline": [{"$merge": {"into": "x"}}]}}]`: "$merge",
	}
	for p, stage := range refused {
		pipeline, err := parsePipeline(p)
		if err != nil {
			t.Fatalf("parsePipeline(%s): %v", p, err)
		}
		want := `aggregation stage "` + stage + `" writes data; this server permits reads only`
		if v := PipelineViolation(pipeline); v != want {
			t.Errorf("PipelineViolation(%s) = %q, want %q", p, v, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := parsePipeline(""); err == nil || err.Error() != "aggregate requires pipeline" {
		t.Errorf("empty pipeline: got %v", err)
	}
	if _, err := parsePipeline(`{"$match": {}}`); err == nil || !strings.HasPrefix(err.Error(), "pipeline must be a JSON array") {
		t.Errorf("object pipeline: got %v", err)
	}
	if _, err := parseDoc("filter", `[1]`); err == nil || !strings.HasPrefix(err.Error(), "filter must be a JSON object") {
		t.Errorf("array filter: got %v", err)
	}
	if d, err := parseDoc("filter", "  "); err != nil || len(d) != 0 {
		t.Errorf("blank filter: got %v, %v", d, err)
	}
}

func TestParseDocKeepsOrderAndTypes(t *testing.T) {
	d, err := parseDoc("sort", `{"b": -1, "a": 1, "_id": {"$oid": "65a1b2c3d4e5f60718293a4b"}}`)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{d[0].Key, d[1].Key, d[2].Key}
	if !reflect.DeepEqual(keys, []string{"b", "a", "_id"}) {
		t.Errorf("keys = %v, want order preserved", keys)
	}
	if _, ok := d[2].Value.(bson.ObjectID); !ok {
		t.Errorf("_id decoded as %T, want bson.ObjectID", d[2].Value)
	}
}

func TestJSON(t *testing.T) {
	oid, _ := bson.ObjectIDFromHex("65a1b2c3d4e5f60718293a4b")
	got, err := JSON(bson.D{{Key: "_id", Value: oid}, {Key: "n", Value: int32(3)}, {Key: "tags", Value: bson.A{"x"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"_id":  map[string]any{"$oid": "65a1b2c3d4e5f60718293a4b"},
		"n":    float64(3),
		"tags": []any{"x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("JSON = %#v, want %#v", got, want)
	}
}
