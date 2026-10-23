# 03 - MCP: database argument for postgres tools

Status: needs-triage
Blocked by: 02

- `run_query`, `export_query`: optional `database` argument; for adapters other than postgres it is ignored (covered by a test).
- `get_schema`, `get_columns`, `get_ddl`: open the connection for `path[0]`.
- Tool descriptions mention that a postgres Profile may expose several databases.
- Read-only screening unchanged (`screen(access, violation)`), applied per call.
- MCP e2e: query two databases through one Profile; refusal for an unknown database.

## Comments
- Decision: `database` passed to a non-postgres adapter is ignored, not refused.
