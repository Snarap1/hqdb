# 02 - A revoked CONNECT stays usable through a pooled connection

Status: needs-triage

## Problem

The pool key is Profile name, Database, and a hash of the Profile table (ADR-0006).
A change on the database side does not change the key.
If an administrator revokes `CONNECT` on a Database (or disables the login, or changes its password), a pooled connection opened before the change keeps working until it idles out (60 s) or is evicted.
Before the pool, the next tool call would have failed on connect.

On postgres the same applies to the Database list of a Profile without `dbname` (ADR-0004): a Database that the login can no longer open is still served from the pool.

## Options

- Accept and document it in ADR-0006 and `docs/config-guide.md`: the window is bounded by the idle TTL, and an open session survives a revoke on the server side anyway.
- Re-check privileges cheaply on checkout (postgres `has_database_privilege(current_database(), 'CONNECT')`), at the cost of a round trip per call.
- Shorten the TTL or make it configurable.

A decision is needed before implementation; the first option is the likely default.

## Comments
