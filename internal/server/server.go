// Package server registers hqdb's MCP tools and serves them over stdio.
package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/catalog"
	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/ddl"
	"github.com/Snarap1/hqdb/internal/export"
	"github.com/Snarap1/hqdb/internal/mask"
	"github.com/Snarap1/hqdb/internal/mongocmd"
	"github.com/Snarap1/hqdb/internal/query"
	"github.com/Snarap1/hqdb/internal/readonly"
	"github.com/Snarap1/hqdb/internal/rediscmd"
)

// Serve runs the stdio MCP server until the transport closes or ctx is
// done, reporting version in the MCP implementation handshake.
func Serve(ctx context.Context, version string) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "hqdb", Version: version}, nil)
	maskKey = mask.NewKey()
	pool = newConnPool(poolIdleTTL, poolMaxIdle, time.Now)
	stop := make(chan struct{})
	go pool.run(poolIdleTTL/4, stop)
	defer pool.close()
	defer close(stop)
	registerTools(server)
	return server.Run(ctx, &mcp.StdioTransport{})
}

// pool keeps connections across tool calls (ADR-0006). Serve sets it; while
// it is nil (handlers called directly in tests) every call opens and closes
// its own connection.
var pool *connPool

// opened is the connection one tool call checked out, with what the call
// needs to know about its Profile.
type opened struct {
	conn adapters.DB
	// access decides whether statements are screened (ADR-0003).
	access config.Access
	// m is nil unless the Profile sets mask = true (ADR-0005).
	m *mask.Masker
	// resets is adapters.ResetsSession of conn.
	resets bool
	// stateful is set by ranStatement.
	stateful bool
	// reset is adapters.ResetSession, replaced in tests.
	reset   func(context.Context, adapters.DB) error
	release func(keep bool)
}

// ranStatement records that the call sent the connection a statement hqdb
// did not write itself, which may have changed session state (see close).
// Handlers call it right before running the statement, after every screen
// passed. hqdb's own Catalog and DDL statements change no session state.
func (o *opened) ranStatement() { o.stateful = true }

// resetTimeout bounds the session reset at the end of a call.
const resetTimeout = 5 * time.Second

// close hands the connection back to the pool, or closes it. After a
// statement, a connection whose adapter resets session state is reset now,
// so a lock or an open transaction ends with the call as it did when every
// call closed its connection; one whose adapter cannot reset is closed. A
// failed reset closes the connection too.
func (o *opened) close() {
	keep := !o.stateful
	if o.stateful && o.resets {
		ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
		keep = o.reset(ctx, o.conn) == nil
		cancel()
	}
	o.release(keep)
}

// openProfile resolves a profile name (or the default profile) and checks
// out a connection for it; the caller defers close. The config, the Access
// mode, and the Masker are resolved afresh on every call, so a config edit
// applies to the next call; only the connection is reused, and only while
// the Profile's parameters are unchanged (ADR-0006). A non-empty database
// selects one Database of a postgres Profile (ADR-0004); every other
// adapter ignores it.
func openProfile(ctx context.Context, name, database string) (*opened, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = cfg.DefaultProfile
	}
	profile, err := cfg.Profile(name)
	if err != nil {
		return nil, err
	}
	access, err := config.ProfileAccess(profile)
	if err != nil {
		return nil, err
	}
	m, err := maskerFor(cfg, profile)
	if err != nil {
		return nil, err
	}
	conn, release, err := checkout(ctx, name, database, profile)
	if err != nil {
		return nil, maskErr(m, err)
	}
	return &opened{conn: conn, access: access, m: m, resets: adapters.ResetsSession(conn), reset: adapters.ResetSession, release: release}, nil
}

// checkout takes a connection from the pool, or opens a plain one that is
// closed on release when there is no pool or the Profile cannot be keyed.
func checkout(ctx context.Context, name, database string, profile map[string]any) (adapters.DB, func(keep bool), error) {
	key, ok := newPoolKey(name, database, profile)
	if pool == nil || !ok {
		conn, err := adapters.OpenDatabase(ctx, profile, database)
		if err != nil {
			return nil, nil, err
		}
		return conn, func(bool) { conn.Close() }, nil
	}
	conn, _, release, err := pool.checkout(key, func() (adapters.DB, error) {
		return adapters.OpenReusable(ctx, profile, database)
	})
	return conn, release, err
}

// screen refuses a statement whose detector reported violation, unless the
// profile is read-write, where statements run unscreened (ADR-0003).
func screen(access config.Access, violation string) error {
	if access == config.ReadWrite || violation == "" {
		return nil
	}
	return fmt.Errorf("Refused (read-only profile): %s", violation)
}

// ProfileInfo is one entry of the list_profiles response; credentials are
// never included.
type ProfileInfo struct {
	Name      string        `json:"name"`
	Adapter   string        `json:"adapter"`
	Access    config.Access `json:"access"`
	IsDefault bool          `json:"is_default"`
	Masked    bool          `json:"masked"`
}

type listProfilesIn struct{}

// ListProfilesOut is the list_profiles response.
type ListProfilesOut struct {
	Profiles []ProfileInfo `json:"profiles"`
}

func listProfiles(ctx context.Context, req *mcp.CallToolRequest, in listProfilesIn) (*mcp.CallToolResult, ListProfilesOut, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, ListProfilesOut{}, err
	}
	out := ListProfilesOut{Profiles: []ProfileInfo{}}
	for _, name := range config.SortedProfileNames(cfg) {
		adapter, err := config.Validate(cfg.Profiles[name])
		if err != nil {
			adapter = config.AdapterName(cfg.Profiles[name])
		}
		access, _ := config.ProfileAccess(cfg.Profiles[name])
		masked, _ := config.ProfileMask(cfg.Profiles[name])
		out.Profiles = append(out.Profiles, ProfileInfo{
			Name:      name,
			Adapter:   adapter,
			Access:    access,
			IsDefault: name == cfg.DefaultProfile,
			Masked:    masked,
		})
	}
	return nil, out, nil
}

type getSchemaIn struct {
	Profile        string   `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Path           []string `json:"path" jsonschema:"path segments drilled into, e.g. [] or [\"hqdb\"] or [\"hqdb\",\"public\"]"`
	NameFilter     string   `json:"name_filter,omitempty" jsonschema:"case-insensitive substring filter on node names"`
	IncludeColumns bool     `json:"include_columns,omitempty" jsonschema:"sample key types when the level contains redis keys"`
}

func getSchemaTool(ctx context.Context, req *mcp.CallToolRequest, in getSchemaIn) (_ *mcp.CallToolResult, _ catalog.SchemaOut, err error) {
	o, err := openProfile(ctx, in.Profile, adapters.PathDatabase(in.Path))
	if err != nil {
		return nil, catalog.SchemaOut{}, err
	}
	defer o.close()
	conn, m := o.conn, o.m
	defer func() { err = maskErr(m, err) }()
	out, err := catalog.GetSchema(ctx, conn, in.Path, in.NameFilter, in.IncludeColumns, catalog.DefaultLimits)
	if err != nil {
		return nil, catalog.SchemaOut{}, err
	}
	return nil, *out, nil
}

type getColumnsIn struct {
	Profile string   `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Path    []string `json:"path" jsonschema:"path ending with the table, e.g. [\"hqdb\",\"public\",\"widgets\"]"`
}

func getColumnsTool(ctx context.Context, req *mcp.CallToolRequest, in getColumnsIn) (_ *mcp.CallToolResult, _ catalog.ColumnsOut, err error) {
	o, err := openProfile(ctx, in.Profile, adapters.PathDatabase(in.Path))
	if err != nil {
		return nil, catalog.ColumnsOut{}, err
	}
	defer o.close()
	conn, m := o.conn, o.m
	defer func() { err = maskErr(m, err) }()
	out, err := catalog.GetColumns(ctx, conn, in.Path)
	if err != nil {
		return nil, catalog.ColumnsOut{}, err
	}
	return nil, *out, nil
}

type getDdlIn struct {
	Profile string   `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Path    []string `json:"path" jsonschema:"path ending with the table or view, e.g. [\"hqdb\",\"public\",\"widgets\"] on postgres, [\"hqdb\",\"widgets\"] on clickhouse"`
}

func getDdlTool(ctx context.Context, req *mcp.CallToolRequest, in getDdlIn) (_ *mcp.CallToolResult, _ ddl.Out, err error) {
	o, err := openProfile(ctx, in.Profile, adapters.PathDatabase(in.Path))
	if err != nil {
		return nil, ddl.Out{}, err
	}
	defer o.close()
	conn, m := o.conn, o.m
	defer func() { err = maskErr(m, err) }()
	out, err := ddl.GetDDL(ctx, conn, in.Path)
	if err != nil {
		return nil, ddl.Out{}, err
	}
	if m != nil {
		out.Definition = m.Text(out.Definition)
	}
	return nil, *out, nil
}

type runQueryIn struct {
	Profile  string `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Database string `json:"database,omitempty" jsonschema:"postgres only: the database to run in, as listed by get_schema with an empty path; omit for the profile's default. Ignored by other adapters"`
	SQL      string `json:"sql" jsonschema:"one SQL statement"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum rows to return; 0 means unlimited (default 50)"`
}

// RunQueryOut is the run_query response.
type RunQueryOut struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"row_count"`
	Truncated bool     `json:"truncated"`
	// MaskedColumns names the columns with a masked value; masked
	// Profiles only (ADR-0005).
	MaskedColumns []string `json:"masked_columns,omitempty"`
}

func runQueryTool(ctx context.Context, req *mcp.CallToolRequest, in runQueryIn) (_ *mcp.CallToolResult, _ RunQueryOut, err error) {
	o, err := openProfile(ctx, in.Profile, in.Database)
	if err != nil {
		return nil, RunQueryOut{}, err
	}
	defer o.close()
	conn, access, m := o.conn, o.access, o.m
	defer func() { err = maskErr(m, err) }()
	sqlConn, ok := conn.(*adapters.SQL)
	if !ok {
		return nil, RunQueryOut{}, adapters.ErrNotSQL(conn.Kind(), "run_query")
	}
	if err := guard(access, m, readonly.Violation(in.SQL), in.SQL); err != nil {
		return nil, RunQueryOut{}, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	o.ranStatement()
	res, err := query.Run(ctx, sqlConn.SQL(), in.SQL, limit)
	if err != nil {
		return nil, RunQueryOut{}, err
	}
	var maskedColumns []string
	if m != nil {
		maskedColumns = m.Rows(res.Columns, res.Rows)
	}
	out := RunQueryOut{
		Columns:       res.Columns,
		Rows:          res.Rows,
		RowCount:      res.RowCount,
		Truncated:     res.Truncated,
		MaskedColumns: maskedColumns,
	}
	return nil, out, nil
}

type runRedisIn struct {
	Profile string   `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Command string   `json:"command" jsonschema:"a redis command with any inline arguments, e.g. \"GET user:1000\""`
	Args    []string `json:"args,omitempty" jsonschema:"further command arguments, e.g. [\"MATCH\",\"user:*\"]"`
}

// RunRedisOut is the run_redis response.
type RunRedisOut struct {
	Command string `json:"command"`
	Result  any    `json:"result"`
}

func runRedisTool(ctx context.Context, req *mcp.CallToolRequest, in runRedisIn) (_ *mcp.CallToolResult, _ RunRedisOut, err error) {
	o, err := openProfile(ctx, in.Profile, "")
	if err != nil {
		return nil, RunRedisOut{}, err
	}
	defer o.close()
	conn, access, m := o.conn, o.access, o.m
	defer func() { err = maskErr(m, err) }()
	redisConn, ok := conn.(*adapters.Redis)
	if !ok {
		return nil, RunRedisOut{}, adapters.ErrNotSQL(conn.Kind(), "run_redis")
	}
	if err := guard(access, m, rediscmd.CommandViolation(in.Command), append([]string{in.Command}, in.Args...)...); err != nil {
		return nil, RunRedisOut{}, err
	}
	// The allowlisted read commands leave no connection state behind
	// (no SELECT, CLIENT, HELLO, MULTI, WATCH, or SUBSCRIBE), so only a
	// command off the allowlist (a read-write Profile) taints the client.
	if rediscmd.CommandViolation(in.Command) != "" {
		o.ranStatement()
	}
	result, err := rediscmd.Run(ctx, redisConn, in.Command, in.Args)
	if err != nil {
		return nil, RunRedisOut{}, err
	}
	out := RunRedisOut{Command: strings.TrimSpace(in.Command), Result: maskRedis(m, in.Command, result)}
	return nil, out, nil
}

type runMongoIn struct {
	Profile    string `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Database   string `json:"database,omitempty" jsonschema:"database name; omit to use the profile's database"`
	Collection string `json:"collection" jsonschema:"collection (or view) name"`
	Operation  string `json:"operation,omitempty" jsonschema:"find, aggregate, count, distinct, insertOne, insertMany, updateOne, or deleteOne (default find); the last four need a read-write profile"`
	Filter     string `json:"filter,omitempty" jsonschema:"query filter as a MongoDB Extended JSON object, e.g. {\"status\": \"active\", \"_id\": {\"$oid\": \"...\"}}"`
	Projection string `json:"projection,omitempty" jsonschema:"find projection as a JSON object, e.g. {\"name\": 1}"`
	Sort       string `json:"sort,omitempty" jsonschema:"find sort as a JSON object, e.g. {\"created_at\": -1}"`
	Pipeline   string `json:"pipeline,omitempty" jsonschema:"aggregate pipeline as a JSON array of stages; $out and $merge are refused"`
	Field      string `json:"field,omitempty" jsonschema:"field name for distinct"`
	Document   string `json:"document,omitempty" jsonschema:"insertOne document as a MongoDB Extended JSON object"`
	Documents  string `json:"documents,omitempty" jsonschema:"insertMany documents as a JSON array of objects"`
	Update     string `json:"update,omitempty" jsonschema:"updateOne update as a JSON object, e.g. {\"$set\": {\"status\": \"done\"}}, or a pipeline array"`
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum documents to return; 0 means unlimited (default 50)"`
}

// RunMongoOut is the run_mongo response.
type RunMongoOut struct {
	Operation string `json:"operation"`
	Documents []any  `json:"documents"`
	Count     int64  `json:"count"`
	Truncated bool   `json:"truncated"`
	// MaskedFields lists the dotted paths of masked fields; masked
	// Profiles only (ADR-0005).
	MaskedFields []string `json:"masked_fields,omitempty"`
}

func runMongoTool(ctx context.Context, req *mcp.CallToolRequest, in runMongoIn) (_ *mcp.CallToolResult, _ RunMongoOut, err error) {
	o, err := openProfile(ctx, in.Profile, "")
	if err != nil {
		return nil, RunMongoOut{}, err
	}
	defer o.close()
	conn, access, m := o.conn, o.access, o.m
	defer func() { err = maskErr(m, err) }()
	mongoConn, ok := conn.(*adapters.Mongo)
	if !ok {
		return nil, RunMongoOut{}, adapters.ErrNotSQL(conn.Kind(), "run_mongo")
	}
	op := mongocmd.NormalizeOperation(in.Operation)
	if err := guard(access, m, mongocmd.OperationViolation(op), in.Filter, in.Projection, in.Sort, in.Pipeline, in.Document, in.Documents, in.Update); err != nil {
		return nil, RunMongoOut{}, err
	}
	if op == "aggregate" {
		pipeline, err := mongocmd.ParsePipeline(in.Pipeline)
		if err != nil {
			return nil, RunMongoOut{}, err
		}
		if err := screen(access, mongocmd.PipelineViolation(pipeline)); err != nil {
			return nil, RunMongoOut{}, err
		}
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	o.ranStatement()
	res, err := mongocmd.Run(ctx, mongoConn, mongocmd.Request{
		Database:   in.Database,
		Collection: in.Collection,
		Operation:  op,
		Filter:     in.Filter,
		Projection: in.Projection,
		Sort:       in.Sort,
		Pipeline:   in.Pipeline,
		Field:      in.Field,
		Document:   in.Document,
		Documents:  in.Documents,
		Update:     in.Update,
		Limit:      limit,
	})
	if err != nil {
		return nil, RunMongoOut{}, err
	}
	docs, maskedFields := maskMongo(m, op, in.Field, res.Documents)
	out := RunMongoOut{Operation: op, Documents: docs, Count: res.Count, Truncated: res.Truncated, MaskedFields: maskedFields}
	return nil, out, nil
}

type exportQueryIn struct {
	Profile  string `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Database string `json:"database,omitempty" jsonschema:"postgres only: the database to run in, as listed by get_schema with an empty path; omit for the profile's default. Ignored by other adapters"`
	SQL      string `json:"sql" jsonschema:"one SQL statement"`
	DestPath string `json:"dest_path" jsonschema:"destination file path; its parent directory must exist"`
	Format   string `json:"format,omitempty" jsonschema:"csv, json, or ndjson; inferred from the file extension when omitted"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum rows to write; 0 means unlimited"`
}

// ExportQueryOut is the export_query response.
type ExportQueryOut struct {
	Path     string `json:"path"`
	Format   string `json:"format"`
	RowCount int    `json:"row_count"`
}

func exportQueryTool(ctx context.Context, req *mcp.CallToolRequest, in exportQueryIn) (_ *mcp.CallToolResult, _ ExportQueryOut, err error) {
	o, err := openProfile(ctx, in.Profile, in.Database)
	if err != nil {
		return nil, ExportQueryOut{}, err
	}
	defer o.close()
	conn, access, m := o.conn, o.access, o.m
	defer func() { err = maskErr(m, err) }()
	sqlConn, ok := conn.(*adapters.SQL)
	if !ok {
		return nil, ExportQueryOut{}, adapters.ErrNotSQL(conn.Kind(), "export_query")
	}
	format := in.Format
	if format == "" {
		if format, err = export.InferFormat(in.DestPath); err != nil {
			return nil, ExportQueryOut{}, err
		}
	}
	if err := guard(access, m, readonly.Violation(in.SQL), in.SQL); err != nil {
		return nil, ExportQueryOut{}, err
	}
	var transform export.RowTransform
	if m != nil {
		transform = m.Row
	}
	o.ranStatement()
	n, err := export.Query(ctx, sqlConn.SQL(), in.SQL, in.DestPath, format, in.Limit, transform)
	if err != nil {
		return nil, ExportQueryOut{}, err
	}
	out := ExportQueryOut{Path: in.DestPath, Format: format, RowCount: n}
	return nil, out, nil
}

// maskNote ends the description of every tool that returns data or takes a
// statement, so the agent knows how a masked Profile behaves (ADR-0005).
const maskNote = " On a masked profile (masked is true in list_profiles) personal data comes back as masked-... markers; " +
	"equal values share a marker within the session, so they can still be joined, grouped, and counted; " +
	"a marker cannot be used in a statement, and a statement that contains one is refused."

// registerTools adds every tool to the server.
func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_profiles",
		Description: "List the database profiles from the hqdb config files, with their adapter, " +
			"access mode (read-only or read-write), whether it is masked, and which one is the default. Never includes passwords.",
	}, listProfiles)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_schema",
		Description: "List database objects one level at a time. Start with an empty path for the " +
			"databases, then drill down: [\"hqdb\"] lists schemas (postgres, mysql, mssql) or tables " +
			"(clickhouse) or collections (mongodb), and [\"hqdb\",\"public\"] lists tables. A postgres profile exposes every database its login can open; the empty path lists them. On redis, keys are grouped by the " +
			"segment after the path prefix, reported as namespace or key nodes.",
	}, getSchemaTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_columns",
		Description: "List the columns of one table: name, type, and nullability. The path's last two segments address the table, e.g. [\"hqdb\",\"public\",\"widgets\"] on postgres, [\"hqdb\",\"widgets\"] on clickhouse. On postgres the first segment is the database (see get_schema with an empty path). On mongodb, fields are inferred from a sample of documents.",
	}, getColumnsTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_ddl",
		Description: "Return the definition of a table or view. mysql and clickhouse return the " +
			"server's own CREATE TABLE/VIEW text; postgres, mssql, and odbc reconstruct a table's " +
			"columns, primary key, foreign keys, indexes, and check/unique constraints from the " +
			"system catalogs (native is false), but return a view's stored definition verbatim " +
			"(native is true). On postgres the path is [database, schema, table], the first segment being " +
			"the database (see get_schema with an empty path). Not available for redis or mongodb." + maskNote,
	}, getDdlTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_query",
		Description: "Run one SQL statement and return rows as JSON. A postgres profile may expose several databases; " +
			"pass database to pick one. On a read-only profile the statement is " +
			"screened first: it must start with SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE, " +
			"must not stack statements, and must not contain write keywords " +
			"(insert, update, delete, drop, truncate, alter, create, call, ...). The SQL is sent to the database " +
			"unmodified; no LIMIT is injected, so use the limit argument to bound the response. " +
			"On a read-write profile any statement runs unscreened." + maskNote,
	}, runQueryTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_redis",
		Description: "Run one Redis command against a redis profile. On a read-only profile only " +
			"allowlisted read commands (get, mget, hgetall, smembers, scan, ttl, type, info, zrange, " +
			"xrange, geosearch, ...) are permitted; writes, blocking commands, eval, subscribe, and " +
			"config are refused. On a read-write profile any command runs." + maskNote,
	}, runRedisTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_mongo",
		Description: "Run one MongoDB operation against a mongodb profile: find (filter, " +
			"projection, sort), aggregate (pipeline), count (filter), distinct (field, filter), " +
			"insertOne (document), insertMany (documents), updateOne (filter, update), or deleteOne " +
			"(filter). Filters, pipelines, documents, and updates are MongoDB Extended JSON strings, " +
			"so {\"$oid\": ...} and {\"$date\": ...} work. The four writing operations and the " +
			"aggregation stages that write ($out, $merge) are refused on a read-only profile." + maskNote,
	}, runMongoTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "export_query",
		Description: "Run one SQL statement (screened like run_query; a postgres profile may expose several databases, pick one with database) and stream the results into a csv, json, or " +
			"ndjson file. The format is inferred from the file extension when not given. json and ndjson rows are " +
			"objects keyed in column order, so every column needs a unique name (alias duplicates such as a.id, b.id). The " +
			"destination's parent directory must exist; it is not created." + maskNote,
	}, exportQueryTool)
}
