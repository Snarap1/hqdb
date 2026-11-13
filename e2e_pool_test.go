package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Snarap1/hqdb/internal/server"
)

// The MCP server reuses connections across tool calls (ADR-0006); these
// cases prove a statement's session state never reaches the next call.

// queryOne runs sql on profile and returns the first row's first cell.
func (s *e2eSession) queryOne(profile, sql string) any {
	s.t.Helper()
	var out server.RunQueryOut
	s.ok("run_query", map[string]any{"profile": profile, "sql": sql}, &out)
	if len(out.Rows) == 0 || len(out.Rows[0]) == 0 {
		return nil
	}
	return out.Rows[0][0]
}

func TestE2EPoolReusesConnection(t *testing.T) {
	s := startE2E(t)
	cases := []struct{ profile, sql string }{
		{"pg-rw", "SELECT pg_backend_pid()"},
		{"ms-rw", "SELECT @@SPID"},
	}
	for _, c := range cases {
		t.Run(c.profile, func(t *testing.T) {
			first := s.queryOne(c.profile, c.sql)
			if second := s.queryOne(c.profile, c.sql); fmt.Sprint(second) != fmt.Sprint(first) {
				t.Errorf("session id %v then %v: the connection was not reused", first, second)
			}
		})
	}
}

func TestE2EPoolSessionStateDoesNotLeak(t *testing.T) {
	s := startE2E(t)
	cases := []struct {
		name    string
		profile string
		set     string
		check   string
		want    string
	}{
		{"postgres SET search_path", "pg-rw", "SET search_path TO pg_catalog", "SHOW search_path", `"$user", public`},
		{"postgres temporary table", "pg-rw", "CREATE TEMP TABLE hqdb_pool_leak (x int)", "SELECT to_regclass('hqdb_pool_leak') IS NULL", "true"},
		{"postgres SET ROLE", "pg-rw", "SET ROLE pg_read_all_data", "SELECT current_user = session_user", "true"},
		{"mysql user variable", "my-rw", "SET @hqdb_pool_leak = 42", "SELECT @hqdb_pool_leak IS NULL", "1"},
		{"mssql open transaction", "ms-rw", "BEGIN TRANSACTION", "SELECT @@TRANCOUNT", "0"},
		{"mssql temporary table", "ms-rw", "CREATE TABLE #hqdb_pool_leak (x int)", "SELECT CASE WHEN OBJECT_ID('tempdb..#hqdb_pool_leak') IS NULL THEN 1 ELSE 0 END", "1"},
		{"mssql SET option", "ms-rw", "SET LANGUAGE Deutsch", "SELECT @@LANGUAGE", "us_english"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s.ok("run_query", map[string]any{"profile": c.profile, "sql": c.set}, nil)
			if got := fmt.Sprint(s.queryOne(c.profile, c.check)); got != c.want {
				t.Errorf("%s after %s = %s, want %s", c.check, c.set, got, c.want)
			}
		})
	}
}

// TestE2EPoolOpenTransactionDoesNotLeak: a BEGIN left open by one call is
// not in effect in the next, where SAVEPOINT needs a transaction block.
func TestE2EPoolOpenTransactionDoesNotLeak(t *testing.T) {
	s := startE2E(t)
	s.ok("run_query", map[string]any{"profile": "pg-rw", "sql": "BEGIN"}, nil)
	msg := s.errText("run_query", map[string]any{"profile": "pg-rw", "sql": "SAVEPOINT hqdb_pool_leak"})
	if !strings.Contains(msg, "transaction blocks") {
		t.Errorf("SAVEPOINT after a BEGIN in the previous call = %q, want the no-transaction-block error", msg)
	}
}

// TestE2EPoolLockEndsWithCall proves the reset runs when the call ends, not
// when the connection is next used: a session lock taken in one call is
// gone for every other session as soon as the call returns.
func TestE2EPoolLockEndsWithCall(t *testing.T) {
	s := startE2E(t)
	s.ok("run_query", map[string]any{"profile": "pg-rw", "sql": "SELECT pg_advisory_lock(4242061)"}, nil)
	// pg-dev is another Profile, so another connection and session.
	held := s.queryOne("pg-dev", "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objid = 4242061")
	if fmt.Sprint(held) != "0" {
		t.Errorf("advisory lock still held after the call: %v locks", held)
	}
}

func TestE2EPoolRedisDatabaseDoesNotLeak(t *testing.T) {
	s := startE2E(t)
	var out server.RunRedisOut
	s.ok("run_redis", map[string]any{"profile": "redis-rw", "command": "SELECT 1"}, &out)
	s.ok("run_redis", map[string]any{"profile": "redis-rw", "command": "CLIENT INFO"}, &out)
	info := fmt.Sprint(out.Result)
	if !strings.Contains(info, " db=0 ") {
		t.Errorf("CLIENT INFO after SELECT 1 = %q, want db=0", info)
	}
}
