package mongoshell

import (
	"strings"
	"testing"

	"github.com/Snarap1/hqdb/internal/mongocmd"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Command
	}{
		{"find bare", "db.users.find()", Command{Request: mongocmd.Request{Collection: "users", Operation: "find"}}},
		{"find filter", "db.users.find({age: {$gt: 30}})", Command{Request: mongocmd.Request{Collection: "users", Operation: "find", Filter: `{"age":{"$gt":30}}`}}},
		{"find filter projection", "db.users.find({name: 'a'}, {_id: 0, name: 1})", Command{Request: mongocmd.Request{Collection: "users", Operation: "find", Filter: `{"name":"a"}`, Projection: `{"_id":0,"name":1}`}}},
		{"find chained", "db.users.find({}).sort({age: -1, name: 1}).skip(5).limit(10)", Command{Request: mongocmd.Request{Collection: "users", Operation: "find", Filter: `{}`, Sort: `{"age":-1,"name":1}`, Skip: 5, Limit: 10}}},
		{"find chained with spaces and semicolon", " db.users.find( { a: ')' } )\n  .limit( 3 ) ; ", Command{Request: mongocmd.Request{Collection: "users", Operation: "find", Filter: `{"a":")"}`, Limit: 3}}},
		{"find regex with paren", "db.users.find({name: /a)b/i})", Command{Request: mongocmd.Request{Collection: "users", Operation: "find", Filter: `{"name":{"$regularExpression":{"pattern":"a)b","options":"i"}}}`}}},
		{"dotted collection", "db.system.views.find()", Command{Request: mongocmd.Request{Collection: "system.views", Operation: "find"}}},
		{"getCollection", "db.getCollection('my-coll').countDocuments()", Command{Request: mongocmd.Request{Collection: "my-coll", Operation: "count"}}},
		{"aggregate", "db.orders.aggregate([{$match: {s: 'x'}}, {$group: {_id: '$k', n: {$sum: 1}}}])", Command{Request: mongocmd.Request{Collection: "orders", Operation: "aggregate", Pipeline: `[{"$match":{"s":"x"}},{"$group":{"_id":"$k","n":{"$sum":1}}}]`}}},
		{"countDocuments", "db.orders.countDocuments({s: 'x'})", Command{Request: mongocmd.Request{Collection: "orders", Operation: "count", Filter: `{"s":"x"}`}}},
		{"distinct", "db.orders.distinct('status')", Command{Request: mongocmd.Request{Collection: "orders", Operation: "distinct", Field: "status"}}},
		{"distinct filter", `db.orders.distinct("status", {n: {$gt: 1}})`, Command{Request: mongocmd.Request{Collection: "orders", Operation: "distinct", Field: "status", Filter: `{"n":{"$gt":1}}`}}},
		{"use", "use other_db", Command{Use: "other_db"}},
		{"use semicolon", "  use other;", Command{Use: "other"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseCommand(c.src)
			if err != nil {
				t.Fatalf("ParseCommand(%q): %v", c.src, err)
			}
			if got != c.want {
				t.Fatalf("ParseCommand(%q)\n got %+v\nwant %+v", c.src, got, c.want)
			}
		})
	}
}

func TestParseCommandUnsupported(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{"empty", "  ", "empty statement"},
		{"insert", "db.users.insertOne({a: 1})", `"insertOne" is not supported`},
		{"not db", "show dbs", "not supported"},
		{"js", "var x = 1", "not supported"},
		{"modifier on aggregate", "db.o.aggregate([]).limit(1)", `".limit()" is not supported after aggregate`},
		{"unknown modifier", "db.o.find().pretty()", `".pretty()" is not supported`},
		{"too many find args", "db.o.find({}, {}, {})", "find takes at most 2 arguments"},
		{"aggregate not array", "db.o.aggregate({$match: {}})", "aggregate takes one pipeline array"},
		{"aggregate options", "db.o.aggregate([], {allowDiskUse: true})", "aggregate takes one pipeline array"},
		{"distinct without field", "db.o.distinct()", "distinct takes a field name"},
		{"filter not object", "db.o.find(1)", "filter must be a document"},
		{"limit not int", "db.o.find().limit('x')", "limit takes a non-negative integer"},
		{"sort not doc", "db.o.find().sort(1)", "sort must be a document"},
		{"unbalanced", "db.o.find({a: 1}", "unbalanced"},
		{"trailing", "db.o.find() x", "unexpected"},
		{"translate error", "db.o.find({a: })", "position"},
		{"use without name", "use", "use takes a database name"},
		{"use two words", "use a b", "use takes a database name"},
		{"no method", "db.users", "not supported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseCommand(c.src)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("ParseCommand(%q) error = %v, want it to contain %q", c.src, err, c.wantErr)
			}
		})
	}
}

// Every "not supported" message names the supported subset.
func TestUnsupportedNamesSupported(t *testing.T) {
	for _, src := range []string{"show dbs", "db.u.insertOne({})", "db.u.find().pretty()"} {
		_, err := ParseCommand(src)
		if err == nil || !strings.Contains(err.Error(), "supported: db.<coll>.find(filter, projection)") {
			t.Fatalf("ParseCommand(%q) error = %v, want the supported list", src, err)
		}
	}
}
