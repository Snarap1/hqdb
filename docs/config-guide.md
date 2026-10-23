# Configuring hqdb

A guide to writing, validating, and debugging hqdb config files.
Everything here is what the `hqdb` binary actually implements, for both of its front ends: the terminal UI (`hqdb`) and the MCP server for AI agents (`hqdb mcp`).
Nothing depends on an agent skill or a package manager.

hqdb is configured entirely by its own TOML files.
It never reads another tool's config files.

## 1. Pick a file

| situation | file |
| --- | --- |
| personal, all projects, one machine | `~/.config/hqdb/config.toml` |
| project-scoped, committed | `<repo>/hqdb.toml` |
| shared inside a Python project | `[tool.hqdb]` in `pyproject.toml` |
| testing a candidate config | `HQDB_CONFIG=/path/to/candidate.toml` |
| CI or a throwaway run | `HQDB_CONFIG` pointing at a generated file |

### Discovery and priority

The server reads every candidate that exists; the last one wins.

1. `$HOME/pyproject.toml`, `$HOME/.hqdb.toml`, `$HOME/hqdb.toml`
2. `$XDG_CONFIG_HOME/hqdb/config.toml`, `.hqdb.toml`, `hqdb.toml` (`$XDG_CONFIG_HOME` is `~/.config` when unset)
3. cwd: `pyproject.toml`, `.hqdb.toml`, `hqdb.toml`
4. `$HQDB_CONFIG`, when set; appended last, so it has the highest priority

Missing files are skipped silently.
`HQDB_CONFIG` is the exception: a set-but-missing path is a hard error, `config file could not be found at specified path: <path>`, not a fallback.

A `pyproject.toml` contributes only its `[tool.hqdb]` table; every other section is ignored.

### Merge semantics

Merge is **top-level key replacement, not a deep merge**.
A higher-priority file's `profiles` table replaces the whole map from every lower-priority file.
A `default_profile` string replaces the previous value.

Consequence: a project-local `hqdb.toml` with one profile silently removes every profile in `~/.config/hqdb/config.toml`.
Keep each file self-contained, or point `HQDB_CONFIG` at one file and rely on its contents alone.

### Credentials

Passwords live in the file in plain text.
Keep a real-credential config out of version control.
A committed example should carry placeholders only.

## 2. The file shape

Only two top-level keys are accepted: `default_profile` and `profiles`.
Anything else, `keymaps` included, is rejected with
`hqdb config files must only contain default_profile and profiles tables (got <key>)`.

```toml
default_profile = "local-pg"

[profiles.local-pg]
adapter = "postgres"
host = "127.0.0.1"
port = 5432
user = "app"
password = "app"
dbname = "appdb"
```

Profile names are TOML table names, so a name with a dot or a space must be quoted: `[profiles."analytics.eu"]`.
Prefer plain lowercase-with-dashes names.

Every profile needs `adapter`.
Valid values: `postgres`, `mysql`, `mssql`, `odbc`, `clickhouse`, `redis`, `mongodb`.
A profile with no `adapter` reads as `duckdb` and fails to open; there is no duckdb adapter.

`default_profile` is optional.
Without it, every call without an explicit `profile` argument returns
`no profile specified and no default_profile is set`.

### Access mode

Every profile, whatever its adapter, takes `access`: `"read-only"` (the default when the key is absent) or `"read-write"`.
Any other value fails with `option "access" must be "read-only" or "read-write" (got "<value>")`.

| | `read-only` | `read-write` |
| --- | --- | --- |
| MCP server (`hqdb mcp`) | screens every statement and refuses writes with `Refused (read-only profile): <reason>` (section 7) | runs statements unscreened |
| terminal UI Consoles | refuse the same statements with the same reason | ask `This will write to <profile>: ... Run? y/N` before a write |
| terminal UI Data view | screens the typed WHERE and ORDER BY | runs them as typed |

```toml
[profiles.dev-pg]
adapter = "postgres"
host = "127.0.0.1"
dbname = "appdb"
access = "read-write"
```

The typical split: analysts get a config whose profiles are all read-only, developers get read-write access to the databases they own and read-only access to production.
The access mode is defense in depth, not a security boundary: whoever can edit the config can change it.
A real read-only setup also connects with a database account that can only read.

### Migration from an older config

Copy the `[profiles.*]` tables into a new `hqdb.toml`.
Do not rename the file: hqdb never reads another tool's config, so a profile the user edits there will not reach the server.
Drop foreign-only keys, in particular a top-level `keymaps` table, because they are rejected rather than ignored.
Redis and MongoDB profiles may be new: they live in the same TOML file as the SQL profiles, and that tradeoff is deliberate to keep one config.

## 3. Adapter keys

Every key inside a profile must be valid for that adapter.
A typo such as `hostname` fails with
`unknown option "hostname" for adapter "postgres"; valid options: access, adapter, conn_str, database, dbname, host, password, port, sslmode, user`.

`database` is an alias of `dbname` for `postgres`, `mysql`, and `mssql`; `dbname` wins when both are present.
`conn_str` is accepted by every adapter except `redis`.
`access` is accepted by every adapter (section 2).

| adapter | keys | defaults |
| --- | --- | --- |
| `postgres` | `host`, `port`, `user`, `password`, `dbname` (`database`), `sslmode`, `conn_str` | host `localhost`, port `5432`, `sslmode = "prefer"` |
| `mysql` | `host`, `port`, `user`, `password`, `dbname` (`database`), `tls`, `conn_str` | host `localhost`, port `3306`, `tls = "false"` |
| `mssql` | `host`, `port`, `user`, `password`, `dbname` (`database`), `encrypt`, `conn_str` | host `localhost`, port `1433`, `encrypt = "disable"` |
| `odbc` | `conn_str` only | required, no default |
| `clickhouse` | `host`, `port`, `user`, `password`, `database` (`dbname` fallback), `protocol`, `secure`, `conn_str` | host `localhost`, port `8123` |
| `redis` | `host`, `port`, `user`, `password`, `database`, `secure`, `separator` | host `localhost`, port `6379`, `database = 0`, `separator = ":"` |
| `mongodb` | `host`, `port`, `user`, `password`, `database`, `auth_source`, `secure`, `conn_str` | host `localhost`, port `27017`, no default database |

An empty postgres host becomes `localhost` rather than a unix socket.

`postgres` takes `dbname` as an optional narrowing (ADR-0004).
Omit it and the profile discovers its databases: hqdb connects to the login's default database (and retries against `postgres` only when that database does not exist or the login lacks `CONNECT` on it; other failures, such as bad credentials, are reported as is), then lists every database that allows connections, is not a template, and that the login holds `CONNECT` on.
The terminal UI shows those databases as the top level under the profile, and `get_schema` with an empty `path` lists them.
Set `dbname` and the profile exposes that one database only, as a deliberate narrowing.
A SQL Console opened on a database node is bound to that database; one opened on the profile node is bound to the login's default database (or `postgres` when that is missing), and its tab names the database once the first statement has opened the session.
Every statement still runs inside exactly one database; PostgreSQL has no cross-database query, so one profile reaches many databases but one statement never spans two.
The access mode is per profile and applies to every discovered database; a database that needs a different mode gets its own profile with `dbname`.
Behind PgBouncer, discovery may list databases the pooler does not route; opening one fails with the driver error.
A profile without `dbname` sees more than its default database, so the real boundary is the database account's privileges; revoke `CONNECT` on databases the account should not open.
The mysql `tls` value is passed to the driver as its `tls` parameter, so use the driver's registered names (`true`, `skip-verify`, `preferred`).
`secure = true` (clickhouse, redis, mongodb) turns on TLS with a minimum of TLS 1.2.
`separator` is what `get_schema` splits redis key names on; match the instance's key convention.

A mongodb `conn_str` must start with `mongodb://` or `mongodb+srv://` and is handed to the driver as-is, so replica sets, SRV records, and URI options all work; its path names the default database when `database` is absent.
Without `conn_str`, `host` and `port` build a single-host URI.
Explicit `user`/`password` win over credentials in the URI, keeping the URI's auth mechanism.
`auth_source` is the database the user is defined in; a user created through `MONGO_INITDB_ROOT_USERNAME` lives in `admin`, so set `auth_source = "admin"` for it, or authentication fails.
`database` is only a default: `run_mongo` takes a `database` argument per call, and `get_schema` lists every non-system database the user can see.

### Examples

```toml
[profiles.local-pg]
adapter = "postgres"
host = "127.0.0.1"
port = 5432
user = "app"
password = "app"
dbname = "appdb"

[profiles.local-my]
adapter = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
password = "app"
dbname = "appdb"

[profiles.local-ms]
adapter = "mssql"
host = "127.0.0.1"
port = 1433
user = "sa"
password = "app"
dbname = "appdb"
encrypt = "disable"

[profiles.local-ch]
adapter = "clickhouse"
host = "127.0.0.1"
port = 8123
protocol = "http"
user = "app"
password = "app"
database = "appdb"

[profiles.local-redis]
adapter = "redis"
host = "127.0.0.1"
port = 6379
database = 0
separator = ":"

[profiles.local-mongo]
adapter = "mongodb"
host = "127.0.0.1"
port = 27017
user = "app"
password = "app"
auth_source = "admin"
database = "appdb"

[profiles.atlas]
adapter = "mongodb"
conn_str = "mongodb+srv://app:secret@cluster0.example.mongodb.net/appdb?retryWrites=true"
```

`testdata/fixtures.toml` in this repo is a loopback-only fixture config for the e2e tests; copy it to `~/.config/hqdb/config.toml` for working `*-dev` profiles against the containers started by `bash testdata/fixtures.sh up`.

## 4. `conn_str` forms

### URL form

For `postgres`, `mysql`, `mssql`, and `clickhouse`, `conn_str` may be a URL whose scheme is `postgres://`, `mysql://`, `sqlserver://`, `http://`, `https://`, or `clickhouse://`.
The path sets the database (`database` for clickhouse, `dbname` otherwise); the authority sets `host`, `port`, `user`, `password`.
URL parts only fill keys absent from the profile: explicit keys always win.

```toml
[profiles.url-pg]
adapter = "postgres"
conn_str = "postgres://app:app@127.0.0.1:5432/appdb?sslmode=require"
sslmode = "verify-full"
```

### ODBC form

An `odbc` profile takes `conn_str` and nothing else; without it the server returns `adapter "odbc" requires conn_str`.

The ODBC driver is never loaded: the server parses the string itself and connects over TDS with go-mssqldb.
Recognized keys, case-insensitive: `Driver` (read but unused), `Server` / `Data Source` / `Addr` / `Address` / `Network Address` (`host[,port]`), `Database` / `Initial Catalog`, `Uid` / `User` / `UserName`, `Pwd` / `Password`, `Encrypt`.
A missing port becomes `1433`; a missing `Server` is the error `ODBC conn_str has no Server: <conn_str>`.
`Encrypt` maps as: `no`, `false`, `0`, `optional` to `disable`; `strict` to `strict`; anything else, including `yes` and `true`, to `true`.

`conn_str` may be a string or a list of strings, joined with a single space.
Real profiles use the list form, and each element ends with `;`:

```toml
[profiles.local-odbc]
adapter = "odbc"
conn_str = [
  "Driver={ODBC Driver 18 for SQL Server};",
  "Server=127.0.0.1,1433;",
  "Database=appdb;",
  "Uid=sa;",
  "Pwd=app;",
  "Encrypt=no;",
]
```

### ClickHouse protocol

`protocol` is `http`, `https`, or `native`; anything else fails with `clickhouse protocol must be "http" or "native" (got "...")`.
When `protocol` is absent the port decides: `8123` and `8443` mean HTTP, everything else native.
Set `protocol = "http"` explicitly whenever a container or tunnel maps some other host port onto 8123, because the port heuristic only recognizes those two numbers.

## 5. Validate against the real binary

`hqdb check` loads the config exactly the way the terminal UI and the MCP server do, then probes every profile with one cheap read: `SELECT 1` for SQL adapters, `PING` for redis, and a root `get_schema` for mongodb, which lists databases and needs no collection.

```sh
hqdb check                        # every profile
hqdb check pg-dev redis-dev       # only these
hqdb check --no-probe             # validate the file without connecting
HQDB_CONFIG=./candidate.toml hqdb check
```

Typical output, one line per profile:

```
mongo-dev  mongodb   read-only   ok
pg-dev     postgres  read-write  ok    (default)
redis-dev  redis     read-only   FAIL  dial tcp 127.0.0.1:56379: connect: connection refused
```

Exit status is 0 only when every selected profile passed, so it drops into CI or a pre-commit hook.
A shape error (`unknown option ...`, a bad `access`) fails even with `--no-probe`; a connection error only shows when probing, which separates the two.
A config that fails to load at all (`hqdb config files must only contain ...`, a missing `HQDB_CONFIG`) is reported before any profile, with exit status 1.

## 6. Register with a client

The client has to launch the binary, and the binary has to see the config.
The server's working directory is part of discovery, so a client launched from a directory containing an unrelated `hqdb.toml` makes that file win over `~/.config/hqdb/config.toml`.
The deterministic escape hatch is an absolute `HQDB_CONFIG` in the server's environment.

Claude Code:

```sh
claude mcp add hqdb -- /abs/path/to/hqdb mcp
claude mcp add hqdb -e HQDB_CONFIG=/abs/path/to/hqdb.toml -- /abs/path/to/hqdb mcp
```

`.claude/settings.json`:

```json
{
  "mcpServers": {
    "hqdb": {
      "command": "/abs/path/to/hqdb",
      "args": ["mcp"],
      "env": {
        "HQDB_CONFIG": "/abs/path/to/hqdb.toml"
      }
    }
  }
}
```

Codex reads `~/.codex/config.toml` at user scope, and `.codex/config.toml` in a project for a trusted project.
It uses TOML, not JSON, and a different table name:

```toml
# <repo>/.codex/config.toml  (project scope)
[mcp_servers.hqdb]
command = "/abs/path/to/hqdb"
args = ["mcp"]
env = { HQDB_CONFIG = "/abs/path/to/hqdb.toml" }
```

`env` is optional; drop it to use the normal discovery order.
`startup_timeout_sec` (default 10) and `tool_timeout_sec` (default 60) sit on the same table when a slow database needs more headroom.

Cursor uses `.cursor/mcp.json` with the same JSON payload as `.mcp.json`.
Gemini, OpenCode, and Copilot also take a stdio server with a `command`, `args`, and an `env` map; only the file name differs.
Whatever the client, the server is `hqdb` with the single argument `mcp`; plain `hqdb` opens the terminal UI and never speaks MCP.
Point `command` at the absolute path of the built binary: the process is spawned directly, without a login shell, so a relative path or shell alias never resolves.

With [APM](https://microsoft.github.io/apm/) installed, the same registration comes from the package in this repository, which declares only the server:

```sh
apm install Snarap1/hqdb/apm --target claude   # project scope, writes <repo>/.mcp.json
apm install Snarap1/hqdb/apm --target codex    # project scope, writes <repo>/.codex/config.toml
apm install -g Snarap1/hqdb/apm --target claude   # user scope, writes ~/.claude.json
apm install -g Snarap1/hqdb/apm --target codex    # user scope, writes ~/.codex/config.toml
```

`-g`/`--global` is the only way to select user scope, and `--target` should always be passed: without it apm resolves the harness from the filesystem and exits rather than guessing.
Add `--dry-run` to preview, and `apm uninstall` to strip the entry back out.
The written command is bare `hqdb mcp` from `PATH`, so the binary has to be linked before the harness starts; add `env` to the entry afterwards to pin one config file.
apm creates the harness directory and the config file when they are missing, so nothing has to exist beforehand.

Then confirm in this order:

1. `list_profiles` with no arguments; expect every profile with its adapter, its access mode, and the default flagged. An empty list means the config was not found at all.
2. `run_query` with `sql` set to `SELECT 1` on the default profile; expect one row. A connection error is a config issue; `hqdb check` shows the same error faster.
3. For redis, `run_redis` with `command` set to `PING`.
4. For mongodb, or a postgres profile without `dbname`, `get_schema` with an empty `path`; expect the databases. An empty list with no error usually means the user has no rights on any non-system database.

## 7. When a config is right but queries fail

The config is not the problem; the screening rules are.
Everything in this section applies to read-only profiles; on a read-write profile `run_query`, `export_query`, `run_redis`, and `run_mongo` run what they are given.
The terminal UI applies the same detectors, so a statement refused here is refused in a read-only profile's Console too.

`run_query` and `export_query` screen every statement before it reaches the database.
Comments, string literals, quoted identifiers, and dollar-quoted bodies are stripped first, so text inside them cannot trigger or satisfy a keyword.

1. The statement must start with `SELECT`, `WITH`, `EXPLAIN`, `SHOW`, `DESCRIBE`, `DESC`, or `TABLE`; anything else is refused with `statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE queries are allowed`.
2. One statement per call; a `;` separating two statements is refused with `multiple statements are not allowed; send one read-only query`.
3. No write keyword may survive anywhere: `delete`, `drop`, `truncate`, `alter`, `create`, `replace`, `merge`, `upsert`, `grant`, `revoke`, `vacuum`, `attach`, `detach`, `copy`, `call`, `do`, `lock`, `rename`, `reindex`, `refresh`, `comment`, `nextval`, `setval`, `insert`, `update`; refused with `statement contains write keyword(s): <sorted list>`.

Because the keywords are whole words, a data-modifying CTE (`WITH x AS (UPDATE ...)`) and `SELECT ... FOR UPDATE` are refused, and so is a column or table whose name is a whole-word keyword such as `comment` or `update`; `updated_at` is fine.
None of these is a bug to work around in the server.

On a postgres profile without `dbname`, `run_query` and `export_query` take an optional `database` argument naming one of the databases `get_schema` lists; omitted means the connection's default database, and non-postgres adapters ignore it.
`get_columns`, `get_ddl`, and `get_schema` take the database as the first `path` element.
A database the profile does not expose (not discovered, or different from its `dbname`) is refused with `database "<name>" is not available for this profile; available: <list>`.

The SQL is sent to the database unmodified; no `LIMIT` is injected, so bound the response with the `limit` argument.
`run_query` defaults to 50 rows, `export_query` to unlimited when `limit` is 0.

`run_redis` is default-deny: the leading word of `command` must be on the allowlist, or the call is refused with `command "<cmd>" is not allowed; this profile permits read commands only`.
An empty command is refused with `empty command`.

Allowed: `ping`, `time`, `info`, `dbsize`, `scan`, `keys`, `type`, `exists`, `ttl`, `pttl`, `strlen`, `get`, `getrange`, `getbit`, `mget`, `bitcount`, `lpos`, `hget`, `hgetall`, `hkeys`, `hlen`, `hmget`, `hvals`, `hscan`, `lrange`, `lindex`, `llen`, `scard`, `smembers`, `sismember`, `smismember`, `srandmember`, `sscan`, `zcard`, `zcount`, `zlexcount`, `zrange`, `zrangebyscore`, `zrangebylex`, `zrevrange`, `zrank`, `zrevrank`, `zscore`, `zmscore`, `zscan`, `xlen`, `xrange`, `xrevrange`, `xinfo`, `object`, `memory`, `randomkey`, `geopos`, `geodist`, `geohash`, `geosearch`.

Refused by omission: every write (`set`, `del`, `expire`, `mset`, `setnx`, `getset`, `incr`, `hset`, `lpush`, `sadd`, ...), all blocking commands, `eval` and `evalsha`, `subscribe` and `monitor`, `config`, `select`, `flush*`, and `shutdown`.

Inline arguments in `command` are split on whitespace, so quoting is not honoured: `GET "user:1000"` looks up a key that includes literal quote characters.
Put arguments with spaces in the `args` array, which is passed through verbatim.

`run_mongo` runs exactly one of four operations, so nothing else reaches the server: `find` (with `filter`, `projection`, `sort`), `aggregate` (with `pipeline`), `count` (with `filter`), and `distinct` (with `field` and `filter`).
Any other `operation` is refused with `operation "<op>" is not supported; supported: find, aggregate, count, distinct`.
`filter`, `projection`, `sort`, and `pipeline` are MongoDB Extended JSON strings, so `{"$oid": "..."}` and `{"$date": "..."}` reach the server as real ObjectIDs and dates, and the key order of `sort` is kept.
The aggregation stages `$out` and `$merge` are refused wherever they appear, including inside `$facet`, `$lookup`, and `$unionWith` sub-pipelines, with `Refused (read-only profile): aggregation stage "$out" writes data; this server permits reads only`.
`limit` defaults to 50 documents; `truncated` says more were available.
Results come back as relaxed Extended JSON: ObjectIDs as `{"$oid": ...}`, dates as `{"$date": ...}`, plain numbers as numbers.
`get_columns` on a collection infers top-level fields from a sample of 100 documents; `type` joins every BSON type seen with `|`, and a field missing or null in any sampled document is nullable.

`get_ddl` returns a table's or view's definition at the path's last one or two segments, the same way `get_columns` addresses a table.
mysql and clickhouse return the server's own `SHOW CREATE TABLE` text, and any view's stored definition is returned the same way, so `native` is `true`.
postgres, mssql, and odbc reconstruct a table's definition from the system catalogs instead: columns, primary key, foreign keys, indexes, and check/unique constraints, so `native` is `false`.
`get_ddl` is not available on redis or mongodb profiles.

Cross-adapter guardrails: `run_query`/`export_query` on a redis profile return `<tool> is not available for redis; use run_redis`, and on a mongodb profile `<tool> is not available for mongodb; use run_mongo`; `run_redis`/`run_mongo` on another adapter return `<tool> is not available for <adapter>`; `get_columns`/`get_ddl` on a redis or mongodb profile return the same `<tool> is not available for <adapter>; use <adapter tool>` shape (`get_columns` on redis points at `get_schema`, `get_ddl` points at `run_redis`/`run_mongo`).

## Troubleshooting

| symptom | cause |
| --- | --- |
| `hqdb config files must only contain ... (got keymaps)` | a foreign top-level key; only `default_profile` and `profiles` exist |
| `unknown option "<key>" for adapter "<a>"; valid options: ...` | typo, or a key from another adapter |
| `adapter "odbc" requires conn_str` | an `odbc` profile with no `conn_str` |
| `config file could not be found at specified path: <path>` | `HQDB_CONFIG` points at a file that does not exist |
| a profile from `~/.config` is missing | a higher-priority `profiles` table replaced the whole map |
| `list_profiles` is empty | the client launched the server from an unexpected working directory, or no candidate file has profiles |
| `no profile specified and no default_profile is set` | no `default_profile`, and the call passed no `profile` |
| `Refused (read-only profile): ...` | the read-only policy, not the config; set `access = "read-write"` if this profile should write |
| `option "access" must be "read-only" or "read-write" ...` | a typo in `access`, e.g. `readwrite` or `rw` |
| the client shows no tools, or a garbled handshake | the client launches `hqdb` without the `mcp` argument |
| mongodb `authentication failed` with the right password | `auth_source` is missing; a root user lives in `admin` |
| `no database given and the profile has no database` | a mongodb profile without `database`; pass `database` to `run_mongo` or set one |
| mongodb `server selection error ... context deadline exceeded` | the host is unreachable within 10s, or `secure`/TLS does not match the server |
