package ui_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// waitForPlain waits for a frame whose text, with ANSI styling and cursor
// movement stripped, contains want; card values are colored per type, so
// a key and its value are not adjacent in the raw bytes.
func waitForPlain(t *testing.T, tm *teatest.TestModel, want string) string {
	t.Helper()
	frame := waitForFrame(t, tm.Output(), func(bts []byte) bool {
		return strings.Contains(ansi.Strip(string(bts)), want)
	})
	return ansi.Strip(string(frame))
}

// sendField focuses a Data view field with key k, clears whatever it was
// pre-filled with (prev), types text, and applies it.
func sendField(tm *teatest.TestModel, k rune, prev, text string) {
	tm.Send(tea.KeyPressMsg{Code: k, Text: string(k)})
	for range []rune(prev) {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	typeText(tm, text)
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestMongoDataViewFilterSortPagingCountCardsGridAndExport(t *testing.T) {
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
	// A unique collection, reached through the tree's name filter, so this
	// test neither perturbs nor depends on the Collections level's order.
	name := fmt.Sprintf("zzmdv%06d", time.Now().UnixNano()%1000000)
	coll := mc.Client().Database("hqdb").Collection(name)
	t.Cleanup(func() {
		_ = coll.Drop(context.Background())
		conn.Close()
	})
	var ids []bson.ObjectID
	docs := make([]any, 35)
	for i := range docs {
		id := bson.NewObjectID()
		ids = append(ids, id)
		docs[i] = bson.D{{Key: "_id", Value: id}, {Key: "name", Value: fmt.Sprintf("item%02d", i+1)}, {Key: "n", Value: i + 1}, {Key: "meta", Value: bson.D{{Key: "k", Value: i + 1}}}}
	}
	if _, err := coll.InsertMany(context.Background(), docs); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	tm := startTUI(t, cfg)
	waitForFrameContaining(t, tm.Output(), "mongo-dev")
	selectNode(t, tm, "mongo-dev")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "hqdb")
	selectNode(t, tm, "hqdb")
	enter(tm)
	waitForFrameContaining(t, tm.Output(), "  collection")
	tm.Send(tea.KeyPressMsg{Code: '/', Text: "/"})
	typeText(tm, name)
	waitForText(t, tm, name+"  coll") // the narrow tree may truncate the tag
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	enter(tm)

	// Cards: nested fields start collapsed.
	frame := waitForPlain(t, tm, "1-30")
	if !strings.Contains(frame, "meta: {1 field}") {
		t.Errorf("cards must show nested meta collapsed:\n%s", frame)
	}

	// Paging and count with no Filter.
	tm.Send(tea.KeyPressMsg{Code: 'n', Text: "n"})
	waitForText(t, tm, "31-35")
	tm.Send(tea.KeyPressMsg{Code: '#', Text: "#"})
	waitForText(t, tm, "31-35 of 35")

	// A regex Filter, counted, then sorted descending.
	sendField(tm, 'f', "", "{ name: /^item0[1-3]$/ }")
	waitForText(t, tm, "1-3 ")
	tm.Send(tea.KeyPressMsg{Code: '#', Text: "#"})
	waitForText(t, tm, "1-3 of 3")
	sendField(tm, 'o', "", "{ n: -1 }")
	waitForScreen(t, tm, func(s string) bool {
		i3, i1 := strings.Index(s, `name: "item03"`), strings.Index(s, `name: "item01"`)
		return strings.Contains(s, "Sort { n: -1 }") && i3 >= 0 && i1 > i3
	})

	// An ObjectId Filter matches exactly one Document.
	sendField(tm, 'f', "{ name: /^item0[1-3]$/ }", fmt.Sprintf("{ _id: ObjectId('%s') }", ids[6].Hex()))
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "1-1 ") && strings.Contains(s, fmt.Sprintf("_id: ObjectId('%s')", ids[6].Hex()))
	})

	// A malformed Filter shows its position and keeps the result.
	sendField(tm, 'f', fmt.Sprintf("{ _id: ObjectId('%s') }", ids[6].Hex()), "{ n: }")
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "at position 5") && strings.Contains(s, fmt.Sprintf("_id: ObjectId('%s')", ids[6].Hex()))
	})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})

	// Enter on the collapsed meta line expands it; fields keep the server
	// order the Documents were inserted with: header, _id, name, n, meta.
	sendKey(tm, tea.KeyDown, 4)
	enter(tm)
	waitForScreen(t, tm, func(s string) bool { return strings.Contains(s, "- meta: {1 field}") && strings.Contains(s, "k: 7") })

	// The grid toggle shows one column per sampled field.
	tm.Send(tea.KeyPressMsg{Code: 'v', Text: "v"})
	waitForScreen(t, tm, func(s string) bool {
		return strings.Contains(s, "view: grid") && strings.Contains(s, "meta") && strings.Contains(s, "item07")
	})
	tm.Send(tea.KeyPressMsg{Code: 'v', Text: "v"})
	waitForScreen(t, tm, func(s string) bool { return strings.Contains(s, "view: cards") })

	// Export writes every matching Document, beyond the page, in Sort order.
	sendField(tm, 'f', fmt.Sprintf("{ _id: ObjectId('%s') }", ids[6].Hex()), "{}")
	waitForScreen(t, tm, func(s string) bool { return strings.Contains(s, "1-30 ") })
	path := filepath.Join(t.TempDir(), "docs.json")
	sendField(tm, 'e', "", path)
	waitForPlain(t, tm, "35 documents written")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("export does not parse: %v", err)
	}
	if len(out) != 35 || out[0]["name"] != "item35" || out[34]["name"] != "item01" {
		t.Errorf("export = %d docs, first %v, last %v", len(out), out[0]["name"], out[len(out)-1]["name"])
	}
}
