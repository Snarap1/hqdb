# 02 - Discovery and per-database connections

Status: needs-triage
Blocked by: 01

- Adapter-level function that, for a postgres Profile without `dbname`, connects (default database, then `postgres` fallback) and returns the discovered database list.
- Way to open a postgres connection for a given database of a Profile, with the `dbname` override applied to the DSN.
- Validation: a requested database must be in the discovered list, or equal the configured `dbname`; refusal message lists the available ones.
- `catalog.relationalSchema`: for postgres, path `[]` returns the discovered databases instead of only `current_database()`; deeper levels run on that database's connection.
- Table-driven unit tests for the DSN override and refusal wording; e2e against the fixture with a second database in `testdata/fixtures.sh`.

## Comments
