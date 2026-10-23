# hqdb

A terminal database client with two front ends over one shared core: a terminal UI for a human and an MCP server for AI agents, both in the one `hqdb` binary.

## Connections

**Profile**:
A named connection target in the hqdb config: an adapter plus where and how to connect.
A Profile is not a database; one Profile can expose many Databases.
_Avoid_: Connection, data source, DB (for this meaning)

**Access mode**:
Whether a Profile is read-only (statements are screened and writes refused, the default) or read-write (statements run unscreened; the terminal UI asks before a write).
_Avoid_: Permission, role, mode (alone)

**Adapter**:
The database engine family a Profile speaks: postgres, mysql, mssql, odbc, clickhouse, redis, or mongodb.
_Avoid_: Driver, dialect, engine

## Catalog

**Catalog**:
The browsable hierarchy of objects a Profile exposes, from Databases down to Columns.
_Avoid_: Metadata, tree (for the data itself)

**Database**:
The top Catalog level under a Profile.
_Avoid_: Catalog (for this level)

**Schema**:
A namespace inside a Database, present only for Adapters that have one (postgres, mssql).
_Avoid_: Namespace, owner

**Table**:
Any row-holding relation in a Schema or Database, including views; its kind is shown, not used to split the hierarchy.
_Avoid_: Relation, object

**Column**:
A named, typed field of a Table, the leaf level of the Catalog.
_Avoid_: Field, attribute

**Collection**:
The mongodb counterpart of a Table.
_Avoid_: Table (for mongodb)

**Document**:
One record of a Collection, the mongodb counterpart of a row.
_Avoid_: Row, record (for mongodb)

**Key prefix**:
A redis Catalog level formed by splitting key names on the Profile's separator.
_Avoid_: Folder, namespace

**DDL**:
The definition text of a Table, either reported natively by the Adapter or reconstructed from the Catalog.
_Avoid_: Schema (for this meaning), create script

## Terminal UI

**Data view**:
A tab showing a page of a Table's rows, with server-side filter and ordering and an in-page text search.
_Avoid_: Preview, grid, table view

**Console**:
A tab with an editor bound to one Profile and a result area below it: SQL statements on a SQL Profile, mongo shell statements on a mongodb Profile, command lines on a redis Profile.
_Avoid_: Query editor, worksheet, REPL
