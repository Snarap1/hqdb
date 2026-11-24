# Secret references in Profiles

Status: ready-for-agent

## Problem

Profile passwords live in the config file in plain text (the `password` key, or inside a `conn_str` URL or ODBC string).
The file is committed, backed up, and read by agents; the password travels with it.

## Decision

Add two reference forms a string value may carry instead of a secret (ADR-0008):

- `${env:NAME}`: the environment variable `NAME`; unset is an error, so a typo fails loudly.
- `${cmd:command}`: the stdout of `command`, run through the shell; a trailing newline is trimmed.
  This reaches `pass`, 1Password (`op`), `vault`, and the OS keychain CLIs without hqdb knowing any of them.

Rules:

- References expand in every string value, including inside a longer `conn_str` and each element of the ODBC list form.
- Anything not matching a form is literal text: a literal password keeps working (backward compatibility).
- Resolution happens only inside the public `adapters.Open*` entries, so both front ends and `hqdb check --probe` get it from one place and the MCP pool keeps hashing the raw Profile table (ADR-0006).
- Command outputs are cached per command for 5 minutes, so adapters whose connections reopen after every call (mysql, clickhouse native, redis) do not spawn a secret manager on each call.
- `hqdb check` validates references without running commands: syntax, and env presence on the machine it runs on.
- No error message ever echoes a resolved secret; `FillFromURL` and `parseODBC` redact credentials before naming a `conn_str`.

## Non-goals

- Encrypted config with a master password: `hqdb mcp` is a headless stdio server with nobody to ask.
- Native OS keychain integration: cgo and per-OS code, against the single Go binary; the `cmd:` form reaches every keychain CLI instead.
- `${...}` escaping: a password containing the exact reference syntax is vanishingly rare and stays configurable through `env:`/`cmd:`.
