---
name: hqdb-config
description: Use when writing, editing, migrating, or debugging an hqdb config file (hqdb.toml, .hqdb.toml, [tool.hqdb] in pyproject.toml, ~/.config/hqdb/config.toml), choosing a Profile's access mode, or registering hqdb mcp with an MCP client. Points at docs/config-guide.md, the authoritative repo guide, and hqdb check, the validator built into the binary.
---

# hqdb config authoring

hqdb is a terminal database client with an MCP server for agents (`hqdb mcp`), written in Go, configured entirely by its own TOML files.
No other tool's config files are ever read.

The reference for this skill is `docs/config-guide.md` at the repository root: file locations, discovery and merge order, access modes, per-adapter key tables, `conn_str` URL and ODBC-list forms, the read-only policy, client registration, and a troubleshooting table.
Read it before writing anything; this file only carries the workflow.

## Workflow

1. Gather the connection facts: adapter, host, port, user, password, database, TLS or encryption requirement, and for redis the key separator convention.
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

1. The top level accepts exactly two keys: `default_profile` and `profiles`.
2. Every profile needs `adapter`: `postgres`, `mysql`, `mssql`, `odbc`, `clickhouse`, `redis`, or `mongodb`.
3. Every key inside a profile must be valid for that adapter; `access` is valid for all of them and takes only `read-only` or `read-write`.
4. Merge is top-level key replacement, not deep merge.
5. `pyproject.toml` contributes only its `[tool.hqdb]` section.
6. Passwords live in the file in plain text.
   Never echo them into a committed config, a log, a chat message, or a committed example; use placeholders.

## When a config is right but queries fail

On a read-only profile the screening rules refuse the statement, with `Refused (read-only profile): <reason>`.
That is the policy working, not a config error; section 7 of the guide carries the exact keyword and command lists.
Do not switch a profile to read-write to get past a refusal without the user's explicit decision.
