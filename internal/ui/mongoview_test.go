package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/values"
)

func TestMongoCardsAndCopyKeepServerFieldOrder(t *testing.T) {
	doc := values.Doc{
		{Key: "zeta", Value: 1.0},
		{Key: "_id", Value: 7.0},
		{Key: "alpha", Value: values.Doc{{Key: "y", Value: 1.0}, {Key: "b", Value: 2.0}}},
	}
	tb := loadedMongoTab(doc)
	v := plainView(tb)
	z, id, a := strings.Index(v, "zeta:"), strings.Index(v, "_id:"), strings.Index(v, "alpha:")
	if z < 0 || id < 0 || a < 0 || !(z < id && id < a) {
		t.Errorf("cards must list fields in server order (zeta, _id, alpha):\n%s", v)
	}
	if got, _ := tb.CopyRowText(); got != `{"zeta":1,"_id":7,"alpha":{"y":1,"b":2}}` {
		t.Errorf("CopyRowText = %s, want server field order", got)
	}
}

// loadedMongoTab is a mongodb Data view with docs already applied as its
// first page.
func loadedMongoTab(docs ...any) *mongoTab {
	tb := newMongoTab("mongo-dev", []string{"hqdb", "widgets"})
	applyMongoDataViewMsg(mongoDataViewMsg{tab: tb, documents: docs})
	return tb
}

func sampleDocs() []any {
	return []any{
		values.Doc{
			{Key: "_id", Value: values.Doc{{Key: "$oid", Value: "65a1b2c3d4e5f60718293a4b"}}},
			{Key: "name", Value: "bolt"},
			{Key: "qty", Value: 12.0},
			{Key: "ok", Value: true},
			{Key: "dims", Value: values.Doc{{Key: "w", Value: 1.5}, {Key: "h", Value: 2.0}}},
			{Key: "at", Value: values.Doc{{Key: "$date", Value: "2024-01-02T03:04:05Z"}}},
		},
		values.Doc{{Key: "_id", Value: values.Doc{{Key: "$oid", Value: "65a1b2c3d4e5f60718293a4c"}}}, {Key: "name", Value: "nut"}, {Key: "tags", Value: []any{"a", "b"}}},
	}
}

func plainView(tb *mongoTab) string { return ansi.Strip(tb.View(100, 30, true)) }

func TestMongoCardsShowShellTypedValuesAndNestedFieldsCollapsed(t *testing.T) {
	v := plainView(loadedMongoTab(sampleDocs()...))
	for _, want := range []string{
		"1-2", "page size 30",
		"_id: ObjectId('65a1b2c3d4e5f60718293a4b')",
		`name: "bolt"`, "qty: 12", "ok: true",
		"at: ISODate('2024-01-02T03:04:05Z')",
		"dims: {2 fields}", "tags: [2 items]",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "w: 1.5") {
		t.Errorf("nested field must start collapsed:\n%s", v)
	}
}

func TestMongoCardsColorValuesByType(t *testing.T) {
	raw := loadedMongoTab(sampleDocs()...).View(100, 30, false)
	for _, styled := range []string{
		mongoStringStyle.Render(`"bolt"`),
		mongoNumberStyle.Render("12"),
		mongoObjectIDStyle.Render("ObjectId('65a1b2c3d4e5f60718293a4b')"),
	} {
		if !strings.Contains(raw, styled) {
			t.Errorf("view missing styled %q", styled)
		}
	}
	if mongoStringStyle.Render("x") == mongoNumberStyle.Render("x") {
		t.Error("strings and numbers must be colored differently")
	}
}

func TestMongoCardEnterExpandsAndCollapsesNestedField(t *testing.T) {
	tb := loadedMongoTab(sampleDocs()...)
	keys := defaultKeyMap()
	// Move the cursor onto the "dims" line, wherever it sits in the card.
	for i := 0; i < 20 && !strings.Contains(tb.cursorLineText(), "dims"); i++ {
		tb.Update(press("j"), keys, nil)
	}
	if !strings.Contains(tb.cursorLineText(), "dims") {
		t.Fatal("cursor never reached the dims line")
	}
	tb.Update(press("enter"), keys, nil)
	if v := plainView(tb); !strings.Contains(v, "w: 1.5") || !strings.Contains(v, "h: 2") {
		t.Fatalf("expanded dims must show its fields:\n%s", v)
	}
	tb.Update(press("enter"), keys, nil)
	if v := plainView(tb); strings.Contains(v, "w: 1.5") {
		t.Fatalf("second enter must collapse dims:\n%s", v)
	}
}

func TestMongoToggleSwitchesToGridOfSampledFieldsAndBack(t *testing.T) {
	tb := loadedMongoTab(sampleDocs()...)
	keys := defaultKeyMap()
	tb.Update(press("v"), keys, nil)
	v := plainView(tb)
	header := ""
	for _, line := range strings.Split(v, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "_id") {
			header = line
			break
		}
	}
	// Columns: _id first, then every field sampled off the page, in
	// first-seen server order.
	fields := strings.Fields(header)
	if strings.Join(fields, ",") != "_id,name,qty,ok,dims,at,tags" {
		t.Fatalf("grid header = %q, view:\n%s", header, v)
	}
	if !strings.Contains(v, "ObjectId('65a1b2c3d") || !strings.Contains(v, "bolt") {
		t.Errorf("grid rows missing values:\n%s", v)
	}
	tb.Update(press("v"), keys, nil)
	if v := plainView(tb); !strings.Contains(v, `name: "bolt"`) {
		t.Errorf("toggling again must return to cards:\n%s", v)
	}
}

func TestMongoMalformedFilterShowsPositionAndKeepsPreviousResult(t *testing.T) {
	tb := loadedMongoTab(sampleDocs()...)
	keys := defaultKeyMap()
	tb.Update(press("f"), keys, nil)
	for _, r := range "{ name: }" {
		tb.Update(press(string(r)), keys, nil)
	}
	if cmd := tb.Update(press("enter"), keys, nil); cmd != nil {
		t.Fatal("a Filter that does not parse must not reload")
	}
	v := plainView(tb)
	if !strings.Contains(v, "at position 8") {
		t.Errorf("view must show the parse error position:\n%s", v)
	}
	if !strings.Contains(v, `name: "bolt"`) {
		t.Errorf("previous result must stay visible:\n%s", v)
	}
	if !tb.EditingText() {
		t.Error("the field must stay open so the Filter can be fixed")
	}
}

func TestMongoValidFilterAndSortReload(t *testing.T) {
	tb := loadedMongoTab(sampleDocs()...)
	keys := defaultKeyMap()
	pool := &connPool{conns: map[string]adapters.DB{}, open: func(string) (adapters.DB, error) {
		return nil, errors.New("offline")
	}}
	tb.Update(press("f"), keys, pool)
	for _, r := range "{ name: /bo/i }" {
		tb.Update(press(string(r)), keys, pool)
	}
	if cmd := tb.Update(press("enter"), keys, pool); cmd == nil {
		t.Fatal("a valid Filter must reload")
	}
	tb.Update(press("o"), keys, pool)
	for _, r := range "{ qty: -1 }" {
		tb.Update(press(string(r)), keys, pool)
	}
	if cmd := tb.Update(press("enter"), keys, pool); cmd == nil {
		t.Fatal("a valid Sort must reload")
	}
	if v := plainView(tb); !strings.Contains(v, "Filter { name: /bo/i }") || !strings.Contains(v, "Sort { qty: -1 }") {
		t.Errorf("applied Filter and Sort must show as typed:\n%s", v)
	}
}

func TestMongoCardCopyKeysCopyFieldAndDocument(t *testing.T) {
	tb := loadedMongoTab(sampleDocs()[1])
	keys := defaultKeyMap()
	for i := 0; i < 10 && !strings.Contains(tb.cursorLineText(), "name"); i++ {
		tb.Update(press("j"), keys, nil)
	}
	if got, ok := tb.CopyText(); !ok || got != "nut" {
		t.Errorf("CopyText = %q, %v; want the field's plain value", got, ok)
	}
	if got, ok := tb.CopyRowText(); !ok || got != `{"_id":{"$oid":"65a1b2c3d4e5f60718293a4c"},"name":"nut","tags":["a","b"]}` {
		t.Errorf("CopyRowText = %q, %v; want the whole Document as JSON", got, ok)
	}
}

func TestMongoTabShowsLoadErrorAndEmptyResult(t *testing.T) {
	tb := newMongoTab("mongo-dev", []string{"hqdb", "widgets"})
	applyMongoDataViewMsg(mongoDataViewMsg{tab: tb, err: errors.New("boom")})
	if v := plainView(tb); !strings.Contains(v, "boom") {
		t.Errorf("view must show the load error:\n%s", v)
	}
	if v := plainView(loadedMongoTab()); !strings.Contains(v, "no documents") {
		t.Errorf("empty page must say so:\n%s", v)
	}
}

func TestMongoLoadCmdReportsConnectErrorFromPool(t *testing.T) {
	wantErr := errors.New("dial tcp: refused")
	pool := &connPool{conns: map[string]adapters.DB{}, open: func(string) (adapters.DB, error) {
		return nil, wantErr
	}}
	tb := newMongoTab("mongo-dev", []string{"hqdb", "widgets"})
	msg := tb.loadCmd(pool)().(mongoDataViewMsg)
	if msg.tab != tb || !errors.Is(msg.err, wantErr) {
		t.Fatalf("got %+v, want the connect failure for this tab", msg)
	}
}

// fakeDataViewDB is a non-mongo adapters.DB, used to exercise the
// "not available for this adapter" path without a network.
type fakeDataViewDB struct{}

func (fakeDataViewDB) Kind() string { return "postgres" }
func (fakeDataViewDB) Close()       {}

func TestMongoLoadCmdReportsErrorForNonMongoConnection(t *testing.T) {
	pool := &connPool{conns: map[string]adapters.DB{}, open: func(string) (adapters.DB, error) {
		return fakeDataViewDB{}, nil
	}}
	tb := newMongoTab("pg-dev", []string{"hqdb", "widgets"})
	msg := tb.loadCmd(pool)().(mongoDataViewMsg)
	if msg.tab != tb || msg.err == nil {
		t.Fatalf("got %+v, want an error for a non-mongodb connection", msg)
	}
}
