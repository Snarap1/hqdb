# 04 - TUI: databases in Catalog, pool keyed by database

Status: needs-triage
Blocked by: 02

- Catalog: database level under a postgres Profile without `dbname`.
- `internal/ui/pool.go`: key connections by (Profile, database); `Drop`/`Reopen` per pair and per Profile.
- SQL Console and Data view opened from a database node bind to it; tab title shows the database.
- `Model.bindAccess` unchanged: access comes from the Profile.
- teatest e2e asserting on rendered frames: two databases visible, a table opens from each.

## Comments
