---
name: hqdb-config
description: Use when writing, editing, migrating, or debugging an hqdb config file (hqdb.toml, .hqdb.toml, [tool.hqdb] in pyproject.toml, ~/.config/hqdb/config.toml), choosing a Profile's access mode, or registering hqdb mcp with an MCP client. Points at docs/config-guide.md, the authoritative repo guide, and hqdb check, the validator built into the binary.
---

# hqdb config authoring

hqdb is a terminal database client with an MCP server for agents (`hqdb mcp`), written in Go, configured entirely by its own TOML files.
No other tool's config files are ever read.

The reference for this skill is `docs/config-guide.md` at the repository root: file locations, discovery and merge order, access modes, per-adapter key tables, `conn_str` URL and ODBC-list forms, the read-only policy, masking, client registration, and a troubleshooting table.
Read it before writing anything; this file only carries the workflow.

## Workflow

1. Gather the connection facts: adapter, host, port, user, password, database, TLS or encryption requirement, and for redis the key separator convention.
   Ask where the password should live: in the file, in an environment variable, or in a secret manager (`pass`, 1Password, vault, the OS keychain).
   A password written in the file keeps working, but a committed or shared config should reference it instead (`${env:NAME}`, `${cmd:command}`); see Credentials in section 1 of the guide.
   When the config already carries literals, `hqdb setup` moves them into a store automatically (the platform keyring through its own CLI, `pass`, or an env file) - offer it before writing placeholders by hand.
2. Ask who the config is for and set `access` per profile: omit it (read-only) unless the user wants writes on that profile, then `access = "read-write"`.
   Never make a production profile read-write unless the user says so explicitly.
3. Pick the file. `~/.config/hqdb/config.toml` for personal machine-wide profiles, a project-local `hqdb.toml` for repo-scoped ones.
   See section 1 of the guide for the full discovery and priority order; the merge is top-level key replacement, so a project file's `profiles` table wipes every lower-priority profile.
4. Write the profile: `[profiles.<name>]` with a required `adapter`, and only keys valid for that adapter.
   Copy the template from section 3 of the guide.
5. Decide `default_profile` only when the user wants one.
6. Validate against the real binary, not by eye:

```sh
HQDB_CONFIG=./hqdb.toml hqdb check
```

It prints one line per profile (adapter, access, `ok` or `FAIL` with the error) after a live probe, and exits non-zero on any failure.
`--no-probe` checks the file shape alone; profile names as arguments narrow the set.

7. Register the server with `hqdb mcp` as the command (section 6 of the guide); plain `hqdb` opens the terminal UI and never speaks MCP.

## Rules that break configs

1. The top level accepts exactly three keys: `default_profile`, `profiles`, and `masking`.
2. Every profile needs `adapter`: `postgres`, `mysql`, `mssql`, `odbc`, `clickhouse`, `redis`, or `mongodb`.
3. Every key inside a profile must be valid for that adapter; `access` is valid for all of them and takes only `read-only` or `read-write`, and `mask` is valid for all of them and takes a boolean.
4. Merge is top-level key replacement, not deep merge.
5. `pyproject.toml` contributes only its `[tool.hqdb]` section.
6. Passwords may live in the file in plain text, or behind a `${env:NAME}` or `${cmd:command}` reference that hqdb expands where a connection is opened.
   Never echo a resolved password into a committed config, a log, or a chat message.
   `hqdb check` verifies references without running commands: every `${env:NAME}` must be set on the machine the check runs on, and `--probe` executes `${cmd:...}` for real.

## Masking

Set `mask = true` on a profile that holds personal data and is reached through `hqdb mcp`, production first; the terminal UI never masks.
The optional top-level `[masking]` table takes only `columns` and `allow`, arrays of globs over normalized column names; a project `[masking]` replaces the global one whole.
Add `columns = ["name"]` when a bare `name` column holds a person's name, because bare `name` is not a built-in rule.
Markers are stable within one MCP session only, and masking is defense in depth: tell the user the boundary is a database login with column privileges.
`hqdb check` reports a `[masking]` line.
Section 8 of the guide carries the config keys; `docs/masking.md` carries what is masked, the rules, the detectors, and the limits.

## Postgres databases

A postgres profile always lists every database the login may connect to; `dbname` is only the database it connects to first.
Set `dbname` to a database the login can open when discovery fails with SQLSTATE 28000 (`pg_hba.conf` rejects the default database and `postgres`).
Hold one profile per server and login; a database that needs its own access mode needs its own login.
MCP clients pick a database with the `database` argument of `run_query` and `export_query`, or the first `path` element of `get_schema`, `get_columns`, and `get_ddl`.
Each statement runs in one database; the account's privileges are the real boundary, and PgBouncer may list databases it does not route.
Section 3 of the guide carries the details.

## When a config is right but queries fail

On a read-only profile the screening rules refuse the statement, with `Refused (read-only profile): <reason>`.
That is the policy working, not a config error; section 7 of the guide carries the exact keyword and command lists.
Do not switch a profile to read-write to get past a refusal without the user's explicit decision.
