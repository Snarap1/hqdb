# 03 - Config, MCP server, export hook, check, e2e

Status: done

01 and 02 are done (`internal/mask`, `internal/mask/detect`).
Implement `../spec.md` sections "Config", "MCP server", and "`hqdb check`".
Add a masked fixture Profile and MCP e2e cases (masked rows, `masked_columns`, linking within a session, error-text masking, the marker refusal on read-only and read-write, an unmasked Profile unchanged), without breaking the concurrent TUI suite.

## Comments

- Config: `masking` is stored raw in `Config.Masking`; `mask` is in every adapter key list and `config.Validate` now also checks it via `ProfileMask`, so `hqdb check` reports a bad `mask` with the other Profile options.
- Export: `export.Query` takes a final `export.RowTransform` (a named `func(columns []string, row []any) []any`); the three writers share one `streamRows` helper that applies it once per row.
- Server: the session key is the package variable `maskKey`, set in `Serve`; `maskerFor` builds a fresh Masker per call from it.
  `openProfile` returns the Masker as a new fourth value; every tool defers `maskErr(m, err)` right after opening, and statement tools call `guard` (marker screen, then access screen).
  Connection-open errors are masked too.
- Redis: `KEYS`, `SCAN`, `RANDOMKEY` replies go through `Text` per string; every other reply through `Doc` (RESP3 hash replies arrive as maps, so field names apply; bare strings use the value rules with an empty column name).
  No `masked_fields` for redis.
  The marker screen covers `command` and `args`.
- Mongo: `distinct` values use `Value(field, v)` and report `masked_fields: [field]`; the marker screen covers filter, projection, sort, pipeline, document, documents, update.
- Check: a `[masking]` line is appended after the Profile lines when the config has the table (`mask.New` validation).
- E2E: ran against a throwaway `postgres:17` on 127.0.0.1:55432 (the full Docker fixture set is not pulled here), 8 pg-based tests passed; the mysql, mssql, clickhouse, redis, and mongo fixtures were not started, so only the masked postgres paths ran end to end.
- Test-only side effects: existing tests that assert the valid-option list gained `mask`.
- Redis and mongo E2E: `e2e_masking_nosql_test.go` adds 6 tests against the Docker fixtures (`redis-masked` and `mongo-masked` Profiles added to the test config in `startE2E`; data seeded and removed through the Go clients under a unique key prefix and collection name).
  They cover GET, HGETALL, HGET, KEYS, SCAN, marker refusal in args, mongo find (nested `contacts.phone`, equal-email markers), distinct, and marker refusal in a filter.
  All passed on the first run; no bug found in `internal/server` or `internal/mask`.
