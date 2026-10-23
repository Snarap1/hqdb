# Repository Guidelines

## Project Overview

`hqdb` is a terminal database client for humans and AI agents, in Go: one binary, one config, two front ends over a shared core.
`hqdb` opens the terminal UI (Bubble Tea v2, `internal/ui`); `hqdb mcp` serves the same Profiles over MCP on stdio (`internal/server`); `hqdb check` validates the config (`internal/check`).
Adapters: `postgres`, `mysql`, `mssql`, `odbc` (TDS, no ODBC driver), `clickhouse`, `redis`, `mongodb`.
It reads only its own TOML files (`hqdb.toml`, `.hqdb.toml`, `$XDG_CONFIG_HOME/hqdb/config.toml`, `[tool.hqdb]` in `pyproject.toml`, `HQDB_CONFIG`).
Every Profile has an **Access mode**, `read-only` (default) or `read-write`, and both front ends obey it (ADR-0003).
Vocabulary (Profile, Catalog, Data view, Console, Access mode) is in `GLOSSARY.md`; decisions are in `docs/adr/`.

## Architecture

One domain package per concern under `internal/`; `main.go` at the root is only argument dispatch.
Dependency direction: `config` ← `adapters` ← {`catalog`, `ddl`, `rediscmd`, `mongocmd`, `check`, `server`}, `values`/`sqlrows` ← {`query`, `export`, `rediscmd`}, `adapters`/`query` ← `dataview` ← `internal/ui`, `sqlrows` ← `console` ← `internal/ui`, `consolestate` ← `internal/ui`.
`internal/ui` never imports `internal/server` (enforced by `internal/ui/arch_test.go`), and nothing imports `server` except `main`.

Screening lives at the front ends, never in the core:
- The core execution functions (`query.Run`, `export.Query`, `rediscmd.Run`, `mongocmd.Run`, `console.Run`) run whatever they are given.
- The detectors are `readonly.Violation` (SQL), `rediscmd.CommandViolation` (default-deny whitelist), and `mongocmd.PipelineViolation` (`$out`/`$merge`).
- `internal/server` calls `screen(access, violation)`: refuse with `Refused (read-only profile): <reason>` on a read-only Profile, run unscreened on a read-write one.
- `internal/ui` Consoles call `consoleCore.runOrAsk`: refuse a violation on a read-only Profile, otherwise ask before any write; the SQL Data view screens its typed WHERE/ORDER BY on a read-only Profile. `Model.bindAccess` hands each new tab its Profile's access.

The MCP server opens a **fresh connection per tool call** and closes it after; the terminal UI keeps one lazy connection per Profile (`internal/ui/pool.go`) plus a dedicated session per SQL Console.
Every terminal UI tab kind implements the `tab` interface (`internal/ui/tab.go`); `adapterui.go` is the one per-Adapter dispatch table.
Visual design follows `docs/ui/TUI database client.html`; colors live only in `theme.go`.

Rules that look like bugs and are not:
- SQL screening matches write keywords as whole words, so `SELECT ... FOR UPDATE` and a column literally named `update` or `comment` are refused on a read-only Profile.
- Config merge is top-level key replacement, not deep merge.
- Profiles are raw `map[string]any` tables read through `config.Opt*` accessors; per-adapter key validation rejects unknown keys with the valid-key list.

## Development Commands

```sh
go build -o hqdb .
go vet ./... && gofmt -l .
go test ./...                       # unit tests, no network
bash testdata/fixtures.sh up        # Docker fixtures on 127.0.0.1 (pg, mysql, mssql, clickhouse, redis, mongo)
HQDB_E2E=1 go test ./...            # MCP e2e (root package) and TUI e2e (internal/ui, teatest)
vhs docs/demo.tape                  # re-record the demo; docs/demo.tape has the ffmpeg step to docs/demo.gif
```

In MCP mode stdout is the protocol channel: diagnostics go to stderr, never `fmt.Print` to stdout.
ClickHouse in this environment works over HTTP (8123/8443) only.
Releases come from `.github/workflows/release.yml` on a `v*` tag, which stamps `main.version`; the installers fetch `hqdb_<os>_<arch>.<tar.gz|zip>` and match the binary by file name.

## Conventions

- Errors are plain `fmt.Errorf`/`errors.New`; refusal and validation message wording is part of the contract, so every change needs a matching table-driven test.
- MCP handlers return a Go `error` (the SDK sets `IsError`), never an error inside the payload.
- English only in messages and docs; each sentence on its own line in prose docs.
- Commit messages carry no `Co-Authored-By` trailer for the agent, even when the harness suggests one.
- `docs/config-guide.md` is the user-facing reference (locations, access modes, adapter keys, `conn_str` forms, policy, registration, troubleshooting); keep it, `README.md`, and `.claude/skills/hqdb-config/` in sync with behavior changes.
- `apm/` only registers `hqdb mcp` with a harness; config material belongs in the guide.

## Testing

- Unit tests sit next to their package: behavioral, table-driven, no network.
- E2E is gated behind `HQDB_E2E=1` and uses only `testdata/fixtures.toml` (loopback hosts, throwaway credentials).
  The MCP suite builds the binary and drives `hqdb mcp` with the SDK client; it adds a read-write `pg-rw` Profile for access-mode cases.
  The TUI suite drives the root model in-process with `teatest`, makes fixture Profiles read-write by default (`loadFixtureConfigAs` for read-only), and asserts on rendered frames only.
- Both suites share the Docker fixtures and run concurrently, so a test asserts on the objects it needs rather than on everything present.

## Agent skills

- Issue tracker: local markdown under `.scratch/<feature-slug>/`; see `docs/agents/issue-tracker.md`.
- Domain docs: `GLOSSARY.md` and `docs/adr/`; see `docs/agents/domain.md`.
