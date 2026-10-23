# Postgres: one Profile, every database on the server

Status: needs-triage

## Problem

A postgres Profile is bound to one database, because a PostgreSQL connection is bound to one database and the server has no cross-database query.
A user with several databases on one server has to write one Profile per database, repeating `host`, `user`, and `password` (config merge does not inherit between Profiles).
mysql and mongodb already expose every database from one Profile, so postgres is the odd one out.

## Proposal

A postgres Profile without `dbname` discovers its databases and exposes all of them.
A postgres Profile with `dbname` keeps today's behavior: that one database only, as a deliberate narrowing.

### Discovery

Connect to the login's default database (no database in the startup message); if that fails, retry against `postgres`.
Then run:

```sql
SELECT datname FROM pg_database
WHERE datallowconn AND NOT datistemplate
  AND has_database_privilege(datname, 'CONNECT')
ORDER BY datname;
```

`pg_database` is readable by every role by default, so no extra grant is needed.
The `CONNECT` check hides databases the login cannot open (for example `rdsadmin` on RDS).

### Execution

Every statement still runs inside exactly one database; this is not cross-database querying.
A connection is opened per (Profile, database) pair.
Access mode is per Profile and applies to every discovered database; a database that needs a different mode gets its own Profile with `dbname`.

### MCP

- `get_schema` with an empty path lists the discovered databases; `[db]`, `[db, schema]` work as today against that database.
- `get_columns`, `get_ddl`: the first path element selects the database.
- `run_query`, `export_query`: new optional `database` argument (same wording as `run_mongo`); omitted means the connection's default database; ignored by non-postgres adapters.
- A `database` that was not discovered (or differs from a configured `dbname`) is refused with a message listing the available databases.

### Terminal UI

- Catalog shows databases as the top level under a postgres Profile: database, schema, table.
- `connPool` keys lazy connections by (Profile, database).
- SQL Console and Data view are bound to one database, shown in the tab title.

## Known limits

- Behind PgBouncer, discovery may list databases the pooler does not route; opening them fails with the driver error.
- A Profile without `dbname` now sees more than its default database; the real boundary remains the database account's privileges.
- mssql has the same one-database-per-connection shape; out of scope here, but the design should not block it.

## Decision record

This changes the behavior of existing postgres Profiles without `dbname`, so it needs an ADR (`docs/adr/0004-...`).

## Issues

- `issues/01-adr.md`
- `issues/02-discovery-and-per-database-connections.md`
- `issues/03-mcp-database-argument.md`
- `issues/04-tui-catalog-databases.md`
- `issues/05-docs.md`
