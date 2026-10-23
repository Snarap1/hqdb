// Package mongocmd runs one of four MongoDB operations: find, aggregate,
// count, or distinct. It does not screen an aggregate pipeline for write
// stages itself; the MCP layer screens before calling Run, using
// ParsePipeline and PipelineViolation (ADR-0003).
package mongocmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/values"
)

// Operations is every operation run_mongo accepts, in the order used by
// error messages.
const Operations = "find, aggregate, count, distinct"

// writeStages are the aggregation stages that write to a collection. They
// are refused wherever they appear, including inside $facet, $lookup, and
// $unionWith sub-pipelines.
var writeStages = map[string]bool{"$out": true, "$merge": true}

// Request is one run_mongo call. Filter, Projection, Sort, and Pipeline are
// MongoDB Extended JSON strings, so key order (which matters for sort) and
// typed values such as {"$oid": "..."} or {"$date": "..."} survive.
type Request struct {
	Database   string
	Collection string
	Operation  string
	Filter     string
	Projection string
	Sort       string
	Pipeline   string
	Field      string
	Limit      int
	// Skip skips that many find results first (paging); the MCP tool never sets it.
	Skip int
}

// Result is the JSON-safe outcome of one operation. Documents holds find and
// aggregate documents or distinct values; Count holds the count result or the
// number of returned documents.
type Result struct {
	Documents []any
	Count     int64
	Truncated bool
}

// PipelineViolation returns a reason the pipeline is refused, or "" if it
// contains no write stage.
func PipelineViolation(pipeline bson.A) string {
	if stage := findWriteStage(pipeline); stage != "" {
		return fmt.Sprintf("aggregation stage %q writes data; this server permits reads only", stage)
	}
	return ""
}

func findWriteStage(v any) string {
	switch t := v.(type) {
	case bson.D:
		for _, e := range t {
			if writeStages[e.Key] {
				return e.Key
			}
			if s := findWriteStage(e.Value); s != "" {
				return s
			}
		}
	case bson.A:
		for _, e := range t {
			if s := findWriteStage(e); s != "" {
				return s
			}
		}
	}
	return ""
}

// parseDoc parses an Extended JSON object; "" is an empty document.
func parseDoc(name, s string) (bson.D, error) {
	if strings.TrimSpace(s) == "" {
		return bson.D{}, nil
	}
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(s), false, &d); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %v", name, err)
	}
	return d, nil
}

// ParsePipeline parses an Extended JSON array of stage documents. The MCP
// layer uses it to parse a pipeline for PipelineViolation before calling
// Run, which parses it again to execute.
func ParsePipeline(s string) (bson.A, error) {
	return parsePipeline(s)
}

// parsePipeline parses an Extended JSON array of stage documents.
func parsePipeline(s string) (bson.A, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("aggregate requires pipeline")
	}
	// UnmarshalExtJSON only decodes documents, so wrap the array in one.
	var wrapper struct {
		Pipeline []bson.D `bson:"p"`
	}
	if err := bson.UnmarshalExtJSON([]byte(`{"p":`+s+`}`), false, &wrapper); err != nil {
		return nil, fmt.Errorf("pipeline must be a JSON array of stage objects: %v", err)
	}
	out := make(bson.A, len(wrapper.Pipeline))
	for i, d := range wrapper.Pipeline {
		out[i] = d
	}
	return out, nil
}

// JSON converts a BSON value into a JSON-safe value via relaxed Extended
// JSON: ObjectIDs become {"$oid": ...}, dates {"$date": ...}, and plain
// numbers stay numbers.
func JSON(v any) (any, error) {
	b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false)
	if err != nil {
		return nil, err
	}
	var out struct {
		V any `json:"v"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out.V, nil
}

// OrderedJSON converts a BSON value like JSON, except that documents become
// values.Doc, keeping the server's field order (the terminal UI shows and
// exports Documents in that order).
func OrderedJSON(v any) (any, error) {
	b, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false)
	if err != nil {
		return nil, err
	}
	out, err := values.DecodeOrdered(b)
	if err != nil {
		return nil, err
	}
	d, _ := out.(values.Doc)
	inner, _ := d.Get("v")
	return inner, nil
}

// converter turns one BSON value into its JSON-safe form (JSON or
// OrderedJSON).
type converter func(any) (any, error)

// Run executes one of the four operations against the request's collection.
// Limit bounds find, aggregate, and distinct results; 0 means unlimited. The
// caller is responsible for screening an aggregate pipeline first.
func Run(ctx context.Context, c *adapters.Mongo, req Request) (*Result, error) {
	return run(ctx, c, req, JSON)
}

// RunOrdered is Run with documents as values.Doc in server field order.
func RunOrdered(ctx context.Context, c *adapters.Mongo, req Request) (*Result, error) {
	return run(ctx, c, req, OrderedJSON)
}

// Each streams the documents of a find or aggregate request to fn, as
// values.Doc in server field order, without holding them all in memory,
// and returns how many it passed. Limit bounds the documents; 0 means
// all. fn's error stops the stream and is returned.
func Each(ctx context.Context, c *adapters.Mongo, req Request, fn func(doc any) error) (int, error) {
	coll, filter, err := target(c, req)
	if err != nil {
		return 0, err
	}
	var cur *mongo.Cursor
	switch strings.ToLower(strings.TrimSpace(req.Operation)) {
	case "find", "":
		opts, err := findOptions(req)
		if err != nil {
			return 0, err
		}
		if req.Limit > 0 {
			opts.SetLimit(int64(req.Limit))
		}
		if cur, err = coll.Find(ctx, filter, opts); err != nil {
			return 0, err
		}
	case "aggregate":
		pipeline, err := parsePipeline(req.Pipeline)
		if err != nil {
			return 0, err
		}
		if cur, err = coll.Aggregate(ctx, pipeline); err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("operation %q cannot stream documents; use find or aggregate", req.Operation)
	}
	defer cur.Close(ctx)
	n := 0
	for cur.Next(ctx) {
		if req.Limit > 0 && n >= req.Limit {
			break
		}
		d, err := OrderedJSON(cur.Current)
		if err != nil {
			return n, err
		}
		if err := fn(d); err != nil {
			return n, err
		}
		n++
	}
	return n, cur.Err()
}

// target resolves the request's collection and parses its filter.
func target(c *adapters.Mongo, req Request) (*mongo.Collection, bson.D, error) {
	database := req.Database
	if database == "" {
		database = c.Database()
	}
	if database == "" {
		return nil, nil, fmt.Errorf("no database given and the profile has no database")
	}
	if req.Collection == "" {
		return nil, nil, fmt.Errorf("collection is required")
	}
	coll := c.Client().Database(database).Collection(req.Collection)
	filter, err := parseDoc("filter", req.Filter)
	if err != nil {
		return nil, nil, err
	}
	return coll, filter, nil
}

func run(ctx context.Context, c *adapters.Mongo, req Request, conv converter) (*Result, error) {
	coll, filter, err := target(c, req)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(req.Operation)) {
	case "find", "":
		return find(ctx, coll, filter, req, conv)
	case "aggregate":
		pipeline, err := parsePipeline(req.Pipeline)
		if err != nil {
			return nil, err
		}
		cur, err := coll.Aggregate(ctx, pipeline)
		if err != nil {
			return nil, err
		}
		return drain(ctx, cur, req.Limit, conv)
	case "count":
		n, err := coll.CountDocuments(ctx, filter)
		if err != nil {
			return nil, err
		}
		return &Result{Documents: []any{}, Count: n}, nil
	case "distinct":
		if req.Field == "" {
			return nil, fmt.Errorf("distinct requires field")
		}
		var vals bson.A
		if err := coll.Distinct(ctx, req.Field, filter).Decode(&vals); err != nil {
			return nil, err
		}
		res := &Result{Documents: []any{}}
		for i, v := range vals {
			if req.Limit > 0 && i >= req.Limit {
				res.Truncated = true
				break
			}
			j, err := conv(v)
			if err != nil {
				return nil, err
			}
			res.Documents = append(res.Documents, j)
		}
		res.Count = int64(len(res.Documents))
		return res, nil
	default:
		return nil, fmt.Errorf("operation %q is not supported; supported: %s", req.Operation, Operations)
	}
}

func find(ctx context.Context, coll *mongo.Collection, filter bson.D, req Request, conv converter) (*Result, error) {
	opts, err := findOptions(req)
	if err != nil {
		return nil, err
	}
	if req.Limit > 0 {
		// One extra document tells whether the result was truncated.
		opts.SetLimit(int64(req.Limit) + 1)
	}
	cur, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	return drain(ctx, cur, req.Limit, conv)
}

// findOptions builds the projection, sort, and skip of a find; the caller
// sets the limit.
func findOptions(req Request) (*options.FindOptionsBuilder, error) {
	opts := options.Find()
	if req.Projection != "" {
		proj, err := parseDoc("projection", req.Projection)
		if err != nil {
			return nil, err
		}
		opts.SetProjection(proj)
	}
	if req.Sort != "" {
		sort, err := parseDoc("sort", req.Sort)
		if err != nil {
			return nil, err
		}
		opts.SetSort(sort)
	}
	if req.Skip > 0 {
		opts.SetSkip(int64(req.Skip))
	}
	return opts, nil
}

// drain reads up to limit documents (0 means all) and closes the cursor.
func drain(ctx context.Context, cur *mongo.Cursor, limit int, conv converter) (*Result, error) {
	defer cur.Close(ctx)
	res := &Result{Documents: []any{}}
	for cur.Next(ctx) {
		if limit > 0 && len(res.Documents) >= limit {
			res.Truncated = true
			break
		}
		j, err := conv(cur.Current)
		if err != nil {
			return nil, err
		}
		res.Documents = append(res.Documents, j)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	res.Count = int64(len(res.Documents))
	return res, nil
}
