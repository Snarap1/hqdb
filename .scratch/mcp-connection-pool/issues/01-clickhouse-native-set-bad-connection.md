# 01 - clickhouse native: SET through run_query fails with bad connection

Status: needs-triage

## Problem

On a clickhouse Profile using the native protocol, `run_query` with `SET max_threads = 3` fails with `driver: bad connection`.
It was observed while adding the pool e2e tests, and the native clickhouse e2e case was dropped because of it.
It is unknown whether this predates the pool (ADR-0006) or the limit cancellation in `sqlrows.Query` (commit "Query: stop the server when the row limit cuts a result short").

## To find out

- Reproduce on `ch-native-dev` from `testdata/fixtures.toml` at the commit before the pool and before the limit cancellation, then on main.
- If it is pre-existing: decide whether `SET` on the native protocol should succeed (clickhouse-go may treat a statement with no result set specially in `QueryContext`) and fix in the adapter.
- If the pool or the cancellation causes it: fix there, and make sure a broken connection is never handed out again and that `driver.ErrBadConn` is not retried after the statement was sent.
- Restore the native clickhouse case in the pool e2e suite.

## Comments
