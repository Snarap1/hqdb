# Postgres database discovery

A postgres Profile without `dbname` discovers its Databases and exposes all of them.
A postgres Profile with `dbname` exposes that one Database only, as a deliberate narrowing.
We chose this because a PostgreSQL connection is bound to one database and the server has no cross-database query, so a user with several databases on one server had to write one Profile per database, repeating `host`, `user`, and `password` (config merge does not inherit between Profiles).
mysql and mongodb already expose every Database from one Profile, so postgres was the odd one out.

## Discovery

hqdb connects to the login's default database (no database in the startup message).
It retries against `postgres` only when that database does not exist (SQLSTATE 3D000) the login lacks `CONNECT` on it (42501), or `pg_hba.conf` has no entry for it (28000, common because the default database is named after the login); any other failure, such as bad credentials or an unreachable server, is reported as is.
It then runs:

```sql
SELECT datname FROM pg_database
WHERE datallowconn AND NOT datistemplate
  AND has_database_privilege(datname, 'CONNECT')
ORDER BY datname;
```

`pg_database` is readable by every role by default, so discovery needs no extra grant.
The `CONNECT` check hides Databases the login cannot open, for example `rdsadmin` on RDS.

## Execution

Every statement still runs inside exactly one Database; this is not cross-database querying.
A connection is opened per (Profile, Database) pair.
The Access mode stays per Profile (ADR-0003) and applies to every discovered Database.
A Database that needs a different mode gets its own Profile with `dbname`.

## Considered options

A `dbnames` list on the Profile was rejected for now: discovery plus `dbname` covers "all" and "one", and a list adds a third form to validate and document.
It can be added later as a filter over the discovered set without changing this decision.
Keeping `dbname` mandatory was rejected because it forces the repeated Profiles described above.

## Consequences

This changes the behavior of existing postgres Profiles without `dbname`: they now see more than their default Database.
The real boundary remains the database account's privileges, as ADR-0003 already says for the Access mode.
Behind PgBouncer, discovery may list Databases the pooler does not route; opening them fails with the driver error.
mssql has the same one-database-per-connection shape; it is out of scope here, but this design does not block it.
