# hqdb - headquarters for your databases

[![CI](https://github.com/Snarap1/hqdb/actions/workflows/ci.yml/badge.svg)](https://github.com/Snarap1/hqdb/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Snarap1/hqdb)](https://github.com/Snarap1/hqdb/releases/latest)

<p align="center">
  <img src="docs/demo.gif" alt="hqdb check, then the terminal UI: a postgres Data view with WHERE and ORDER BY, a write refused on a read-only Profile and confirmed on a read-write one, and mongodb Documents as cards" width="800">
</p>

`hqdb` is a terminal database client for humans and AI agents.
One binary, one config, two front ends over the same core:

- `hqdb` opens a full-screen terminal UI: a DataGrip-style tree of every Profile, Data views with server-side filter and paging, DDL, and Consoles for SQL, mongosh, and Redis.
- `hqdb mcp` serves the same Profiles to an AI agent (Claude Code, Codex, Cursor, ...) over MCP on stdio: schema browsing, DDL, queries, and exports.

Adapters: `postgres`, `mysql`, `mssql`, `odbc`, `clickhouse`, `redis`, `mongodb`.

Every Profile is either `read-only` (the default) or `read-write`, and both front ends obey it.
Hand your analysts a config whose Profiles are read-only, and your developers one with read-write access to the databases they own.

## Install

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Snarap1/hqdb/main/install.sh | sh
```

Windows (PowerShell, no admin rights):

```powershell
irm https://raw.githubusercontent.com/Snarap1/hqdb/main/install.ps1 | iex
```

With Go 1.25+:

```sh
go install github.com/Snarap1/hqdb@latest
```

The scripts download the release build for your platform, verify its SHA-256 against the release `checksums.txt`, and install `hqdb` into `~/.local/bin` (Windows: `%LOCALAPPDATA%\Programs\hqdb`, added to the user `PATH`).
Pin a release with `--version v1.0.0` (`$env:HQDB_VERSION` on Windows) and pick another directory with `--dir` (`$env:HQDB_INSTALL_DIR`).
They never touch an MCP client config; registering the server is a separate step.

## Quick start

Write `~/.config/hqdb/config.toml`:

```toml
default_profile = "local-pg"

[profiles.local-pg]
adapter = "postgres"
host = "127.0.0.1"
port = 5432
user = "app"
password = "app"
dbname = "appdb"
access = "read-write"   # omit for read-only

[profiles.prod-pg]
adapter = "postgres"
conn_str = "postgres://analyst@db.example.com:5432/app"

[profiles.cache]
adapter = "redis"
host = "127.0.0.1"
```

Check it, then open it:

```sh
hqdb check      # one line per Profile: adapter, access, and a live probe
hqdb            # the terminal UI
```

```
cache     redis     read-only   ok
local-pg  postgres  read-write  ok  (default)
prod-pg   postgres  read-only   ok
```

`hqdb check --no-probe` validates the file without connecting; `hqdb check prod-pg` checks one Profile.
It exits non-zero on any failure, so it drops into CI too.

## Access modes

| | `read-only` (default) | `read-write` |
| --- | --- | --- |
| MCP server | Screens every statement and refuses writes with `Refused (read-only profile): <reason>` | Runs statements unscreened |
| Terminal UI Consoles | Refuses the same statements with the same reason | Asks `This will write to <profile>: ... Run? y/N` before a write |
| Terminal UI Data view | Screens the typed WHERE and ORDER BY | Runs them as typed |

The screening for SQL is defense in depth: the statement must start with `SELECT`, `WITH`, `EXPLAIN`, `SHOW`, `DESCRIBE`, `DESC`, or `TABLE`, stacked statements are refused, and no write keyword (`insert`, `update`, `delete`, `drop`, `truncate`, `alter`, `create`, `call`, ...) may appear outside comments and strings.
Redis is default-deny: allowlisted read commands only.
MongoDB refuses the writing aggregation stages `$out` and `$merge`.
The full lists are in [`docs/config-guide.md`](docs/config-guide.md#7-when-a-config-is-right-but-queries-fail).

The access mode is not a security boundary: anyone who can edit the config can change it.
For a real read-only setup, also connect with a database account that can only read.

## Terminal UI

```sh
hqdb
```

The left pane lists every Profile; expand one to browse Databases, Schemas, Tables, Collections, or Redis key prefixes.
`Enter` on a Table opens its Data view, `d` its DDL, and `c` on a Profile opens a Console.
Console buffers and statement history persist per Profile under `$XDG_STATE_HOME/hqdb`.
Single-letter keys work only when focus is not in a text field; `Esc` leaves a text field, and `?` shows every binding.

| Scope | Key | Action |
|---|---|---|
| global | `z` | hide or show the tree |
| global | `Tab` / `Shift+Tab` | move focus between tree and active tab |
| global | `[` / `]` | previous or next tab |
| global | `x` | close the active tab |
| global | `?` | keymap help |
| global | `q` | quit |
| global | `Ctrl+C` | cancel the running work in the focused pane (never quits) |
| tree | `Up`/`k`, `Down`/`j` | move the selection |
| tree | `Enter` / double-click | expand, or open the Data view on a Table |
| tree | `d` | DDL of the Table |
| tree | `c` | Console of the Profile |
| tree | `r` | refresh the node or reconnect the Profile |
| tree | `D` | disconnect the Profile |
| tree | `/` | filter nodes by name |
| Data view | `f` / `o` | focus WHERE (Filter) / ORDER BY (Sort) |
| Data view | `n` / `p` | next / previous page |
| Data view | `s` | page size |
| Data view | `#` | count matching rows |
| Data view, SQL Console results | `e` | export to csv, json, or ndjson |
| mongodb Data view, mongodb Console results | `v` | toggle cards / grid |
| grid | arrows, `h`/`j`/`k`/`l` | move the cell cursor |
| grid | `/` | search in the loaded page |
| grid | `Ctrl+N` / `Ctrl+P` | next / previous search match |
| grid | `Enter` / double-click | cell popup |
| cards | `Enter` / double-click | expand or collapse a nested field |
| grid, cards | `y` | copy cell (cards: the field, or the whole Document on its header) |
| grid, cards | `Y` | copy row (cards: Document) as JSON |
| Console | `Alt+Enter` | run the statement under the cursor (Redis: the line) |
| Console editor | `Ctrl+Up` / `Ctrl+Down` | older / newer statement from history |
| Console editor | `Esc` | leave the editor for the results |
| Console results | `i` | back into the editor |

## MCP server for AI agents

```sh
hqdb mcp
```

The server speaks MCP over stdio: stdout is the protocol channel and every diagnostic goes to stderr.
Connections are opened per tool call and closed afterwards, so it runs happily next to a terminal UI session.

| tool | what it does |
| --- | --- |
| `list_profiles` | Lists Profile names, adapters, access modes, and the default; never returns passwords. |
| `get_schema` | Lists database objects one level at a time; drill down with `path`. A postgres Profile without `dbname` lists its Databases first. |
| `get_columns` | Lists the columns of one table: name, type, nullability. MongoDB fields are inferred from a sample. |
| `get_ddl` | Returns a table's or view's definition, native where the server has one, reconstructed otherwise. |
| `run_query` | Runs one SQL statement and returns rows as JSON; `database` picks a postgres Database. |
| `run_redis` | Runs one Redis command. |
| `run_mongo` | Runs `find`, `aggregate`, `count`, or `distinct`. |
| `export_query` | Streams a query into a `csv`, `json`, or `ndjson` file; `database` picks a postgres Database. |

`profile` is optional everywhere: omitting it uses `default_profile`.
A postgres Profile without `dbname` discovers every Database the login may connect to; with `dbname` it exposes that one only.
`get_columns` and `get_ddl` take the Database as the first `path` element, and `run_query` and `export_query` take an optional `database` argument (the connection's default when omitted).
Each statement runs inside one Database, never across two; the database account's privileges stay the real boundary, and behind PgBouncer a listed Database may fail to open.
No `LIMIT` is injected into SQL; the `limit` argument bounds the response (default 50).

Register it with Claude Code:

```sh
claude mcp add hqdb -- hqdb mcp
```

Or with Codex, in `~/.codex/config.toml`:

```toml
[mcp_servers.hqdb]
command = "hqdb"
args = ["mcp"]
```

Clients spawn the process without a login shell, so use an absolute path to `hqdb` when it is not on the `PATH` the client sees.
Add `HQDB_CONFIG=/abs/path/hqdb.toml` to the server's environment to pin one config file.
[`docs/config-guide.md`](docs/config-guide.md#6-register-with-a-client) has snippets for more clients, and [`apm/`](apm/README.md) registers the server through [APM](https://microsoft.github.io/apm/).

## Config

`hqdb` reads its own TOML files, in priority order (later wins):

1. `~/pyproject.toml` (`[tool.hqdb]` only), `~/.hqdb.toml`, `~/hqdb.toml`
2. `$XDG_CONFIG_HOME/hqdb/config.toml`, `.hqdb.toml`, `hqdb.toml`
3. the working directory: `pyproject.toml`, `.hqdb.toml`, `hqdb.toml`
4. `$HQDB_CONFIG`, when set

The merge is top-level key replacement: a higher-priority file's `profiles` table replaces the whole map.
Only `default_profile` and `profiles` are accepted at the top level.

| adapter | keys |
| --- | --- |
| `postgres` | `host`, `port`, `user`, `password`, `dbname` (alias `database`; omit to expose every Database the login can open), `sslmode` (default `prefer`), `conn_str` |
| `mysql` | `host`, `port` (default 3306), `user`, `password`, `dbname` (alias `database`), `tls` (default `false`), `conn_str` |
| `mssql` | `host`, `port` (default 1433), `user`, `password`, `dbname` (alias `database`), `encrypt` (default `disable`), `conn_str` |
| `odbc` | `conn_str` (ODBC string, or a list of strings) |
| `clickhouse` | `host`, `port` (default 8123), `user`, `password`, `database`, `protocol` (`http` or `native`), `secure`, `conn_str` |
| `redis` | `host` (default `localhost`), `port` (default 6379), `user`, `password`, `database`, `secure`, `separator` (default `:`) |
| `mongodb` | `host` (default `localhost`), `port` (default 27017), `user`, `password`, `database`, `auth_source`, `secure`, `conn_str` (`mongodb://` or `mongodb+srv://`) |

Every adapter also takes `access` (`read-only` or `read-write`).
An unknown key is rejected with the list of valid options for that adapter.
[`docs/config-guide.md`](docs/config-guide.md) is the full guide: choosing a file, `conn_str` URL and ODBC-list forms, ClickHouse HTTP vs native, client registration, and troubleshooting.
It is self-contained, so you can also hand an agent the link and let it write the config.

## Build and test

Requires Go 1.25.

```sh
go build -o hqdb .
go vet ./... && gofmt -l .
go test ./...                          # unit tests, no network
bash testdata/fixtures.sh up           # Docker fixtures on 127.0.0.1
HQDB_E2E=1 go test ./...               # MCP and terminal UI e2e against them
bash testdata/fixtures.sh down
```

`testdata/fixtures.toml` is a loopback-only config for those fixtures; point `HQDB_CONFIG` at it to try `hqdb` against them.
`docs/demo.tape` records `docs/demo.gif` with [VHS](https://github.com/charmbracelet/vhs) and ffmpeg against the same fixtures; the tape header has the two commands.

Releases are cut by pushing a `v*` tag; `.github/workflows/release.yml` builds static binaries for linux, darwin, and windows and attaches them with `checksums.txt`.
