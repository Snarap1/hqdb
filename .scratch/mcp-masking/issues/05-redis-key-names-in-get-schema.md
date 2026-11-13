# 05 - Redis key names in get_schema are not masked

Status: ready-for-agent

## Problem

On a masked redis Profile, `run_redis KEYS`/`SCAN`/`RANDOMKEY` mask key names string by string, so `user:ivan@x.ru` becomes `user:masked-<12 hex>`.
`get_schema` on the same Profile returns the key names of a level verbatim in `structuredContent`, because `docs/masking.md` states that `get_schema` and `get_columns` "return names and types only and are never masked".
For SQL and mongodb that holds: names are identifiers chosen by a developer.
For redis it does not: a key name is data, often built from an email, phone, or ID.
An agent can read every key name in clear through `get_schema` and bypass the `run_redis` masking entirely.

## Expected

- On a masked redis Profile, `get_schema` masks each key name the same way `run_redis KEYS` does (segment by segment, same markers within the session).
- The key groups (prefix tree levels) are masked consistently, so a masked group name expands to the same masked children.
- A path passed back by the agent that contains a marker is refused by the marker screen, as for statements.
- `docs/masking.md` "What is masked" table gets a `get_schema` row for redis; the "never masked" sentence is narrowed to SQL and mongodb.

## Notes

- Found during the MCP pool work on 2026-11-13; pre-existing, not introduced by the pool.
- Check whether the terminal path through `catalog` needs anything: it must not, the terminal UI never masks (ADR-0005).
- Table-driven tests in `internal/server` with a fake or the e2e redis fixture (`HQDB_E2E=1`).

## Comments
