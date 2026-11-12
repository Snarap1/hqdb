package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Snarap1/hqdb/internal/server"
)

// The redis and mongo masking cases seed their own uniquely named objects
// through the Go clients (the masked profiles are never written through) and
// remove them afterwards.

func isMarker(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "masked-") && len(s) == len("masked-")+12
}

func seedRedis(t *testing.T) (*redis.Client, string) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:56379"})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, fmt.Sprintf("hqdbmask:%d", time.Now().UnixNano())
}

func TestE2EMaskedRedisStringAndHash(t *testing.T) {
	s := startE2E(t)
	rdb, prefix := seedRedis(t)
	ctx := context.Background()
	strKey, hashKey := prefix+":str", prefix+":hash"
	t.Cleanup(func() { rdb.Del(context.Background(), strKey, hashKey) })
	if err := rdb.Set(ctx, strKey, "ivan@example.com", 5*time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.HSet(ctx, hashKey, "phone", "+49 151 23456789", "email", "ivan@example.com", "city", "Berlin").Err(); err != nil {
		t.Fatal(err)
	}

	var got server.RunRedisOut
	s.ok("run_redis", map[string]any{"profile": "redis-masked", "command": "GET", "args": []string{strKey}}, &got)
	if !isMarker(got.Result) {
		t.Errorf("GET = %v, want a masked-<12 hex> marker", got.Result)
	}

	var clear server.RunRedisOut
	s.ok("run_redis", map[string]any{"profile": "redis-dev", "command": "GET", "args": []string{strKey}}, &clear)
	if clear.Result != "ivan@example.com" {
		t.Errorf("unmasked GET = %v", clear.Result)
	}

	var all server.RunRedisOut
	s.ok("run_redis", map[string]any{"profile": "redis-masked", "command": "HGETALL", "args": []string{hashKey}}, &all)
	h, ok := all.Result.(map[string]any)
	if !ok {
		t.Fatalf("HGETALL result = %#v, want an object", all.Result)
	}
	if !isMarker(h["phone"]) || !isMarker(h["email"]) || h["city"] != "Berlin" {
		t.Errorf("HGETALL = %v, want phone and email masked and city in clear", h)
	}

	for field, wantMasked := range map[string]bool{"phone": true, "email": true, "city": false} {
		var one server.RunRedisOut
		s.ok("run_redis", map[string]any{"profile": "redis-masked", "command": "HGET", "args": []string{hashKey, field}}, &one)
		if isMarker(one.Result) != wantMasked {
			t.Errorf("HGET %s = %v, masked want %v", field, one.Result, wantMasked)
		}
	}
}

func TestE2EMaskedRedisKeysAndScan(t *testing.T) {
	s := startE2E(t)
	rdb, prefix := seedRedis(t)
	key := prefix + ":ivan@example.com"
	t.Cleanup(func() { rdb.Del(context.Background(), key) })
	if err := rdb.Set(context.Background(), key, "x", 5*time.Minute).Err(); err != nil {
		t.Fatal(err)
	}

	check := func(name string, result any) {
		t.Helper()
		list, _ := result.([]any)
		var found string
		for _, e := range list {
			if k, _ := e.(string); strings.HasPrefix(k, prefix+":") {
				found = k
			}
		}
		if found == "" {
			t.Fatalf("%s = %v, want a key with prefix %s", name, result, prefix)
		}
		if strings.Contains(found, "ivan@example.com") || !strings.Contains(found, "masked-") {
			t.Errorf("%s key = %q, want the email replaced by a marker", name, found)
		}
	}
	var keys server.RunRedisOut
	s.ok("run_redis", map[string]any{"profile": "redis-masked", "command": "KEYS", "args": []string{prefix + ":*"}}, &keys)
	check("KEYS", keys.Result)

	var scan server.RunRedisOut
	s.ok("run_redis", map[string]any{
		"profile": "redis-masked", "command": "SCAN",
		"args": []string{"0", "MATCH", prefix + ":*", "COUNT", "10000"},
	}, &scan)
	// SCAN replies [cursor, [keys...]]; the cursor is a string of digits.
	if parts, _ := scan.Result.([]any); len(parts) == 2 {
		check("SCAN", parts[1])
	} else {
		t.Fatalf("SCAN = %#v", scan.Result)
	}
}

func TestE2EMaskedRedisRefusesMarkerInArgs(t *testing.T) {
	s := startE2E(t)
	got := s.errText("run_redis", map[string]any{
		"profile": "redis-masked", "command": "GET", "args": []string{"masked-0123456789ab"},
	})
	if want := "Refused (masked profile): statement contains a masked value ("; !strings.HasPrefix(got, want) {
		t.Errorf("got %q, want prefix %q", got, want)
	}
}

func seedMongo(t *testing.T) (*mongo.Collection, string) {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://hq:hq@127.0.0.1:57017/?authSource=admin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	name := fmt.Sprintf("mcp_mask_%d", time.Now().UnixNano())
	coll := client.Database("hqdb").Collection(name)
	t.Cleanup(func() { _ = coll.Drop(context.Background()) })
	_, err = coll.InsertMany(context.Background(), []any{
		bson.M{"_id": 1, "name_first": "Ivan", "email": "ivan@example.com", "contacts": bson.M{"phone": "+49 151 23456789"}, "city": "Berlin"},
		bson.M{"_id": 2, "name_first": "Ivan", "email": "ivan@example.com", "contacts": bson.M{"phone": "+49 151 23456789"}, "city": "Berlin"},
		bson.M{"_id": 3, "name_first": "Olga", "email": "olga@example.com", "contacts": bson.M{"phone": "+49 151 99999999"}, "city": "Bonn"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return coll, name
}

func TestE2EMaskedMongoFindMasksFields(t *testing.T) {
	s := startE2E(t)
	_, name := seedMongo(t)
	var out server.RunMongoOut
	s.ok("run_mongo", map[string]any{"profile": "mongo-masked", "collection": name, "sort": `{"_id": 1}`}, &out)
	if len(out.Documents) != 3 {
		t.Fatalf("documents = %v", out.Documents)
	}
	docs := make([]map[string]any, len(out.Documents))
	for i, d := range out.Documents {
		docs[i], _ = d.(map[string]any)
		contacts, _ := docs[i]["contacts"].(map[string]any)
		if !isMarker(docs[i]["email"]) || !isMarker(contacts["phone"]) {
			t.Errorf("doc %d = %v, want email and contacts.phone masked", i, docs[i])
		}
		if docs[i]["city"] == nil || isMarker(docs[i]["city"]) {
			t.Errorf("doc %d city = %v, want it in clear", i, docs[i]["city"])
		}
	}
	if docs[0]["email"] != docs[1]["email"] {
		t.Errorf("equal emails must share a marker: %v vs %v", docs[0]["email"], docs[1]["email"])
	}
	if docs[0]["email"] == docs[2]["email"] {
		t.Errorf("different emails must not share a marker: %v", docs[0]["email"])
	}
	if want := []string{"contacts.phone", "email"}; !containsAll(out.MaskedFields, want) {
		t.Errorf("masked_fields = %v, want it to include %v", out.MaskedFields, want)
	}
}

func containsAll(have, want []string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func TestE2EMaskedMongoDistinct(t *testing.T) {
	s := startE2E(t)
	_, name := seedMongo(t)
	var out server.RunMongoOut
	s.ok("run_mongo", map[string]any{"profile": "mongo-masked", "collection": name, "operation": "distinct", "field": "email"}, &out)
	if len(out.Documents) != 2 {
		t.Fatalf("documents = %v, want two distinct markers", out.Documents)
	}
	for _, d := range out.Documents {
		if !isMarker(d) {
			t.Errorf("distinct value %v, want a marker", d)
		}
	}
	if want := []string{"email"}; !reflect.DeepEqual(out.MaskedFields, want) {
		t.Errorf("masked_fields = %v, want %v", out.MaskedFields, want)
	}
}

func TestE2EMaskedMongoRefusesMarkerInFilter(t *testing.T) {
	s := startE2E(t)
	got := s.errText("run_mongo", map[string]any{
		"profile": "mongo-masked", "collection": "widgets", "filter": `{"email": "masked-0123456789ab"}`,
	})
	if want := "Refused (masked profile): statement contains a masked value ("; !strings.HasPrefix(got, want) {
		t.Errorf("got %q, want prefix %q", got, want)
	}
}
