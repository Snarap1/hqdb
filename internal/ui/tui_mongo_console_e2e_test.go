package ui_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Snarap1/hqdb/internal/adapters"
)

func TestMongoConsoleRunsShellCommandsUseAndConfirmsWrites(t *testing.T) {
	cfg := loadFixtureConfig(t, "")
	prof, err := cfg.Profile("mongo-dev")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := adapters.Open(prof)
	if err != nil {
		t.Fatalf("connecting to mongo-dev to seed a fixture: %v", err)
	}
	mc := conn.(*adapters.Mongo)
	// Unique names, so concurrent tests on the shared fixture neither see
	// nor disturb these; everything is dropped in cleanup.
	suffix := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	name := "zzmcon" + suffix
	outName := "zzmconout" + suffix
	otherDB := "zzmcondb" + suffix
	coll := mc.Client().Database("hqdb").Collection(name)
	other := mc.Client().Database(otherDB).Collection("things")
	t.Cleanup(func() {
		ctx := context.Background()
		_ = coll.Drop(ctx)
		_ = mc.Client().Database("hqdb").Collection(outName).Drop(ctx)
		_ = mc.Client().Database(otherDB).Drop(ctx)
		conn.Close()
	})
	docs := make([]any, 5)
	for i := range docs {
		docs[i] = bson.D{{Key: "name", Value: fmt.Sprintf("item%02d", i+1)}, {Key: "n", Value: i + 1}, {Key: "kind", Value: []string{"even", "odd"}[(i+1)%2]}}
	}
	if _, err := coll.InsertMany(context.Background(), docs); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if _, err := other.InsertOne(context.Background(), bson.D{{Key: "label", Value: "elsewhere" + suffix}}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "mongo-dev")
	selectNode(t, tm, "mongo-dev")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitForPlain(t, tm, "mongo-dev  mongodb console · db: hqdb")

	// find with a projection, sort, skip, and limit, shown as cards.
	typeText(tm, "db."+name+".find({n: {$gt: 1}}, {_id: 0, name: 1}).sort({n: -1}).skip(1).limit(2);")
	altEnter(tm)
	waitForScreen(t, tm, func(s string) bool {
		i4, i3 := strings.Index(s, `name: "item04"`), strings.Index(s, `name: "item03"`)
		return strings.Contains(s, "2 documents") && i4 >= 0 && i3 > i4 && !strings.Contains(s, `"item05"`)
	})

	runInConsole(tm, "db."+name+".countDocuments({kind: 'odd'})")
	waitForPlain(t, tm, "count: 3")

	runInConsole(tm, "db."+name+".distinct('kind')")
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "2 values") && strings.Contains(s, `"even"`) && strings.Contains(s, `"odd"`)
	})

	runInConsole(tm, "db."+name+".aggregate([{$group: {_id: '$kind', total: {$sum: '$n'}}}, {$sort: {_id: 1}}])")
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "2 documents") && strings.Contains(s, "total: 6") && strings.Contains(s, "total: 9")
	})

	// Unsupported constructs name the supported subset.
	runInConsole(tm, "db."+name+".insertOne({a: 1})")
	waitForPlain(t, tm, "is not supported; supported:")

	// An aggregation with $out prompts; n does not run it, y does.
	out := "db." + name + ".aggregate([{$match: {}}, {$out: '" + outName + "'}])"
	runInConsole(tm, out)
	waitForPlain(t, tm, "This will write to mongo-dev:")
	tm.Send(tea.KeyPressMsg{Code: 'n', Text: "n"})
	waitForPlain(t, tm, "Cancelled")
	runInConsole(tm, "db."+outName+".countDocuments()")
	waitForPlain(t, tm, "count: 0")
	runInConsole(tm, out)
	confirmWrite(t, tm)
	waitForPlain(t, tm, "0 documents")
	runInConsole(tm, "db."+outName+".countDocuments()")
	waitForPlain(t, tm, "count: 5")

	// use switches the Database.
	runInConsole(tm, "use "+otherDB)
	waitForPlain(t, tm, "switched to db "+otherDB)
	runInConsole(tm, "db.things.find()")
	waitForPlain(t, tm, "elsewhere"+suffix)
}
