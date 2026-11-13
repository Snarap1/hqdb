# MCP connection pool with session reset

The MCP server used to open a fresh connection for every tool call and close it afterwards.
It now keeps opened connections inside the `hqdb mcp` process and reuses them for later calls, resetting session state before a reused connection serves a new call.
We chose this because the connect dominated every call: on the local fixtures a fresh connect plus one round trip took 19-23 ms on postgres against 0.19 ms on a pooled connection (mysql 5.5-6 against 0.29, mssql 7-8 against 0.47, mongodb 17 against 0.6), and against a remote postgres the connect alone costs 100-300 ms, while an agent issues many small calls in a row.

## What is reused and what is not

Only the connection is reused.
Every call still loads the config, resolves the Profile, reads its Access mode, and builds its Masker from the session key and the freshly loaded `[masking]` table, so ADR-0003 and ADR-0005 are unchanged and a config edit applies to the next call.
Screening still runs on every statement before it is sent.

## Keying and eviction

A pooled connection is keyed by Profile name, Database, and a SHA-256 of the Profile table it was opened from.
Any edit of the Profile therefore produces a new key, and the first call under the new key closes every idle connection of that Profile opened from the old table; a connection checked out across the edit is closed when it is released.
A connection is checked out by one call at a time; concurrent calls on the same key each get their own.
An idle connection is closed after 60 seconds, the least recently released one is closed when more than 16 are idle, and every idle connection is closed when the server shuts down.

## Session reset per adapter

A database session carries state that a statement can change: `SET`, temporary tables, an unfinished `BEGIN`, advisory or named locks, the selected database.
None of it may reach a later call, so each adapter either resets the session or the connection is not reused after a statement that could have changed it.
Statements hqdb writes itself (the Catalog and DDL queries) change no session state, so a connection is reused after them without a reset.
After any other statement (`run_query`, `export_query`, `run_mongo`, and `run_redis` off the allowlist) the reset runs when the call ends, before the connection goes back to the pool.
Resetting then rather than on the next use keeps what closing the connection used to give: a lock or an open transaction ends with the call, and no other session waits on an idle pooled connection.

- postgres: every idle physical connection runs `DISCARD ALL`, then clears pgx's statement caches, which name the prepared statements `DISCARD ALL` dropped.
  A connection inside a transaction block is discarded instead, because `DISCARD ALL` cannot run there and closing the connection is what rolls the transaction back.
- mssql and odbc: go-mssqldb sets the TDS reset-connection flag on the first request of a reused connection, so hqdb sends each idle connection one `SELECT 1`, and the server resets the session (including an open transaction) before running it.
- clickhouse over HTTP: clickhouse-go sends no `session_id`, so every request is its own session and there is nothing to reset.
- mongodb: the client is pooled; operations carry no session state between calls.
- mysql: go-sql-driver has no way to send `COM_RESET_CONNECTION`, and no SQL statement resets user variables, temporary tables, and named locks together.
  A mysql connection is reused after Catalog and DDL calls only; after `run_query` or `export_query` it is closed.
- clickhouse over the native protocol: a TCP session keeps `SET` and temporary tables and has no reset command, so it is treated like mysql.
- redis: `RESET` would also drop authentication and the selected database, so there is no reliable reset.
  The read-only allowlist holds no command that changes connection state (no `SELECT`, `CLIENT`, `HELLO`, `MULTI`, `WATCH`, or `SUBSCRIBE`), so a client is reused after allowlisted commands and closed after any other command.

If a reset fails, the connection is closed, and the next call opens a new one.
As a second line, a postgres physical connection that missed a reset (its reset epoch is old) runs it before its next statement.

## Failure handling

A pooled connection can die while idle, for example when the server restarts.
The drivers detect that before the statement is sent: pgx through its liveness ping, go-sql-driver through its liveness check on checkout, go-mssqldb when writing the request fails, and go-redis through its health check on an idle connection.
They report `driver.ErrBadConn`, and only then does database/sql retry the statement on a new connection; the contract of that error is that nothing was sent.
A statement that fails after it was sent is returned as an error and never run again, because it may have written.
For the same reason a pooled redis client is opened with retries off: go-redis otherwise retries a command after a network error, which can run a write twice.

## Considered options

Keeping one connection per Profile for the whole session, like the terminal UI, was rejected: an agent's statements would then share session state across calls, and a config edit would need a reconnect rule anyway.
Resetting only when a connection is next used (database/sql's own `ResetSession` hook) was tried first and rejected: an advisory lock or an open transaction then outlived the call for up to the idle timeout.
Resetting after every call, Catalog calls included, was rejected as two needless round trips per call against a remote server.
A `ROLLBACK` before reuse instead of discarding a connection with an open transaction was rejected: closing the connection is simpler and certain, and an open transaction at the end of a call is rare.
Emulating a mysql reset with `ROLLBACK`, `RELEASE_ALL_LOCKS()`, and a sweep of user variables was rejected: temporary tables cannot be listed, so it would not be complete.

## Consequences

A Profile's discovered Database list (ADR-0004) is checked when a connection is opened, so a Database whose `CONNECT` was revoked may stay reachable through a pooled connection for up to the idle timeout.
A server-side idle timeout shorter than 60 seconds, such as a proxy that drops idle connections, usually costs only a reconnect, through the liveness checks above; when the drop is noticed only after the request was written (a half-open TCP connection), the call fails with the driver error and the next call reconnects.
A call that ran a statement pays for the reset when it ends: two round trips per idle connection on postgres, one on mssql.
On the local fixtures `run_query SELECT 1` went from a median of 25.8 to 4.2 ms on postgres, 9.4 to 5.5 on mssql, 9.2 to 4.0 on clickhouse over HTTP, 3.7 to 0.8 for `run_redis PING`, and 22.5 to 2.5 for a mongodb count; mysql and native clickhouse stay at a reconnect per `run_query` (7.5 and 8.1 ms before, 8.9 and 6.3 after), while their Catalog calls are pooled (mysql `get_columns` 25 to 3.6 ms).
The terminal UI is unaffected: it keeps its own connections (`internal/ui/pool.go`).
