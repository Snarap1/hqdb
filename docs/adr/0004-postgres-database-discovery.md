# Postgres database discovery

A postgres Profile discovers its Databases and exposes all of them.
`dbname` is the Database it connects to first, not a narrowing (see Revision).
We chose this because a PostgreSQL connection is bound to one database and the server has no cross-database query, so a user with several databases on one server had to write one Profile per database, repeating `host`, `user`, and `password` (config merge does not inherit between Profiles).
mysql and mongodb already expose every Database from one Profile, so postgres was the odd one out.

## Discovery

hqdb connects to `dbname` when the Profile sets it, with no retry.
Without it, hqdb connects to the login's default database (no database in the startup message).
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
A Database that needs a different mode needs a separate login whose privileges cover only that Database.

## Considered options

A `dbnames` list on the Profile was rejected for now: it adds a form to validate and document.
It can be added later as a filter over the discovered set without changing this decision.
Keeping `dbname` mandatory was rejected because it forces the repeated Profiles described above.

## Consequences

This changes the behavior of existing postgres Profiles: they now see more than their `dbname` or default Database.
The real boundary remains the database account's privileges, as ADR-0003 already says for the Access mode.
Behind PgBouncer, discovery may list Databases the pooler does not route; opening them fails with the driver error.
mssql has the same one-database-per-connection shape; it is out of scope here, but this design does not block it.

## Revision: `dbname` is the starting Database

At first `dbname` narrowed a Profile to that one Database, and only a Profile without it discovered.
That failed on servers whose `pg_hba.conf` admits a login to some Databases only: both the login's default Database (named after the login) and `postgres` are rejected with SQLSTATE 28000, so discovery could not start, and naming a Database that works gave up the others.
`pg_database` is shared by every Database of a server, so discovery works from any Database the login can open.
So `dbname` became the Database discovery starts from, and every postgres Profile discovers.
A separate `maintenance_db` key (the pgAdmin name) for the starting Database was rejected: it would sit beside `dbname` with an overlapping meaning, and narrowing to one Database through hqdb is weaker than the account's own privileges anyway.
Narrowing a Profile to one Database is now done with the login's privileges, by revoking `CONNECT` on the others.
