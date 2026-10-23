// Package server registers hqdb's MCP tools and serves them over stdio.
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Snarap1/hqdb/internal/adapters"
	"github.com/Snarap1/hqdb/internal/catalog"
	"github.com/Snarap1/hqdb/internal/config"
	"github.com/Snarap1/hqdb/internal/ddl"
	"github.com/Snarap1/hqdb/internal/export"
	"github.com/Snarap1/hqdb/internal/mongocmd"
	"github.com/Snarap1/hqdb/internal/query"
	"github.com/Snarap1/hqdb/internal/readonly"
	"github.com/Snarap1/hqdb/internal/rediscmd"
)

// Serve runs the stdio MCP server until the transport closes or ctx is
// done, reporting version in the MCP implementation handshake.
func Serve(ctx context.Context, version string) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "hqdb", Version: version}, nil)
	registerTools(server)
	return server.Run(ctx, &mcp.StdioTransport{})
}

// openProfile resolves a profile name (or the default profile), opens a
// connection, and closes it when the returned function is called. It also
// returns the profile's access mode, which decides whether statements are
// screened (ADR-0003). A non-empty database selects one Database of a
// postgres Profile (ADR-0004); every other adapter ignores it.
func openProfile(ctx context.Context, name, database string) (adapters.DB, config.Access, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", nil, err
	}
	profile, err := cfg.Profile(name)
	if err != nil {
		return nil, "", nil, err
	}
	access, err := config.ProfileAccess(profile)
	if err != nil {
		return nil, "", nil, err
	}
	conn, err := adapters.OpenDatabase(ctx, profile, database)
	if err != nil {
		return nil, "", nil, err
	}
	return conn, access, conn.Close, nil
}

// pathDatabase is the Database a get_columns or get_ddl path names: its
// first segment when the path is database-qualified (database, schema,
// table). Only postgres acts on it; the others ignore it.
func pathDatabase(path []string) string {
	if len(path) >= 3 {
		return path[0]
	}
	return ""
}

// firstSegment is the Database a get_schema path names (path[0]), or "" for
// the root listing.
func firstSegment(path []string) string {
	if len(path) > 0 {
		return path[0]
	}
	return ""
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
		out.Profiles = append(out.Profiles, ProfileInfo{
			Name:      name,
			Adapter:   adapter,
			Access:    access,
			IsDefault: name == cfg.DefaultProfile,
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

func getSchemaTool(ctx context.Context, req *mcp.CallToolRequest, in getSchemaIn) (*mcp.CallToolResult, catalog.SchemaOut, error) {
	conn, _, closeConn, err := openProfile(ctx, in.Profile, firstSegment(in.Path))
	if err != nil {
		return nil, catalog.SchemaOut{}, err
	}
	defer closeConn()
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

func getColumnsTool(ctx context.Context, req *mcp.CallToolRequest, in getColumnsIn) (*mcp.CallToolResult, catalog.ColumnsOut, error) {
	conn, _, closeConn, err := openProfile(ctx, in.Profile, pathDatabase(in.Path))
	if err != nil {
		return nil, catalog.ColumnsOut{}, err
	}
	defer closeConn()
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

func getDdlTool(ctx context.Context, req *mcp.CallToolRequest, in getDdlIn) (*mcp.CallToolResult, ddl.Out, error) {
	conn, _, closeConn, err := openProfile(ctx, in.Profile, pathDatabase(in.Path))
	if err != nil {
		return nil, ddl.Out{}, err
	}
	defer closeConn()
	out, err := ddl.GetDDL(ctx, conn, in.Path)
	if err != nil {
		return nil, ddl.Out{}, err
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
}

func runQueryTool(ctx context.Context, req *mcp.CallToolRequest, in runQueryIn) (*mcp.CallToolResult, RunQueryOut, error) {
	conn, access, closeConn, err := openProfile(ctx, in.Profile, in.Database)
	if err != nil {
		return nil, RunQueryOut{}, err
	}
	defer closeConn()
	sqlConn, ok := conn.(*adapters.SQL)
	if !ok {
		return nil, RunQueryOut{}, adapters.ErrNotSQL(conn.Kind(), "run_query")
	}
	if err := screen(access, readonly.Violation(in.SQL)); err != nil {
		return nil, RunQueryOut{}, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	res, err := query.Run(ctx, sqlConn.SQL(), in.SQL, limit)
	if err != nil {
		return nil, RunQueryOut{}, err
	}
	return nil, RunQueryOut{
		Columns:   res.Columns,
		Rows:      res.Rows,
		RowCount:  res.RowCount,
		Truncated: res.Truncated,
	}, nil
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

func runRedisTool(ctx context.Context, req *mcp.CallToolRequest, in runRedisIn) (*mcp.CallToolResult, RunRedisOut, error) {
	conn, access, closeConn, err := openProfile(ctx, in.Profile, "")
	if err != nil {
		return nil, RunRedisOut{}, err
	}
	defer closeConn()
	redisConn, ok := conn.(*adapters.Redis)
	if !ok {
		return nil, RunRedisOut{}, adapters.ErrNotSQL(conn.Kind(), "run_redis")
	}
	if err := screen(access, rediscmd.CommandViolation(in.Command)); err != nil {
		return nil, RunRedisOut{}, err
	}
	result, err := rediscmd.Run(ctx, redisConn, in.Command, in.Args)
	if err != nil {
		return nil, RunRedisOut{}, err
	}
	return nil, RunRedisOut{Command: strings.TrimSpace(in.Command), Result: result}, nil
}

type runMongoIn struct {
	Profile    string `json:"profile,omitempty" jsonschema:"profile name; omit to use default_profile"`
	Database   string `json:"database,omitempty" jsonschema:"database name; omit to use the profile's database"`
	Collection string `json:"collection" jsonschema:"collection (or view) name"`
	Operation  string `json:"operation,omitempty" jsonschema:"find, aggregate, count, or distinct (default find)"`
	Filter     string `json:"filter,omitempty" jsonschema:"query filter as a MongoDB Extended JSON object, e.g. {\"status\": \"active\", \"_id\": {\"$oid\": \"...\"}}"`
	Projection string `json:"projection,omitempty" jsonschema:"find projection as a JSON object, e.g. {\"name\": 1}"`
	Sort       string `json:"sort,omitempty" jsonschema:"find sort as a JSON object, e.g. {\"created_at\": -1}"`
	Pipeline   string `json:"pipeline,omitempty" jsonschema:"aggregate pipeline as a JSON array of stages; $out and $merge are refused"`
	Field      string `json:"field,omitempty" jsonschema:"field name for distinct"`
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum documents to return; 0 means unlimited (default 50)"`
}

// RunMongoOut is the run_mongo response.
type RunMongoOut struct {
	Operation string `json:"operation"`
	Documents []any  `json:"documents"`
	Count     int64  `json:"count"`
	Truncated bool   `json:"truncated"`
}

func runMongoTool(ctx context.Context, req *mcp.CallToolRequest, in runMongoIn) (*mcp.CallToolResult, RunMongoOut, error) {
	conn, access, closeConn, err := openProfile(ctx, in.Profile, "")
	if err != nil {
		return nil, RunMongoOut{}, err
	}
	defer closeConn()
	mongoConn, ok := conn.(*adapters.Mongo)
	if !ok {
		return nil, RunMongoOut{}, adapters.ErrNotSQL(conn.Kind(), "run_mongo")
	}
	op := strings.ToLower(strings.TrimSpace(in.Operation))
	if op == "" {
		op = "find"
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
	res, err := mongocmd.Run(ctx, mongoConn, mongocmd.Request{
		Database:   in.Database,
		Collection: in.Collection,
		Operation:  op,
		Filter:     in.Filter,
		Projection: in.Projection,
		Sort:       in.Sort,
		Pipeline:   in.Pipeline,
		Field:      in.Field,
		Limit:      limit,
	})
	if err != nil {
		return nil, RunMongoOut{}, err
	}
	return nil, RunMongoOut{Operation: op, Documents: res.Documents, Count: res.Count, Truncated: res.Truncated}, nil
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

func exportQueryTool(ctx context.Context, req *mcp.CallToolRequest, in exportQueryIn) (*mcp.CallToolResult, ExportQueryOut, error) {
	conn, access, closeConn, err := openProfile(ctx, in.Profile, in.Database)
	if err != nil {
		return nil, ExportQueryOut{}, err
	}
	defer closeConn()
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
	if err := screen(access, readonly.Violation(in.SQL)); err != nil {
		return nil, ExportQueryOut{}, err
	}
	n, err := export.Query(ctx, sqlConn.SQL(), in.SQL, in.DestPath, format, in.Limit)
	if err != nil {
		return nil, ExportQueryOut{}, err
	}
	return nil, ExportQueryOut{Path: in.DestPath, Format: format, RowCount: n}, nil
}

// registerTools adds every tool to the server.
func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_profiles",
		Description: "List the database profiles from the hqdb config files, with their adapter, " +
			"access mode (read-only or read-write), and which one is the default. Never includes passwords.",
	}, listProfiles)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_schema",
		Description: "List database objects one level at a time. Start with an empty path for the " +
			"databases, then drill down: [\"hqdb\"] lists schemas (postgres, mysql, mssql) or tables " +
			"(clickhouse) or collections (mongodb), and [\"hqdb\",\"public\"] lists tables. A postgres profile without dbname may expose several databases; the empty path lists them. On redis, keys are grouped by the " +
			"segment after the path prefix, reported as namespace or key nodes.",
	}, getSchemaTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_columns",
		Description: "List the columns of one table: name, type, and nullability. The path's last two segments address the table, e.g. [\"hqdb\",\"public\",\"widgets\"] on postgres, [\"hqdb\",\"widgets\"] on clickhouse. On mongodb, fields are inferred from a sample of documents.",
	}, getColumnsTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_ddl",
		Description: "Return the definition of a table or view. mysql and clickhouse return the " +
			"server's own CREATE TABLE/VIEW text; postgres, mssql, and odbc reconstruct a table's " +
			"columns, primary key, foreign keys, indexes, and check/unique constraints from the " +
			"system catalogs (native is false), but return a view's stored definition verbatim " +
			"(native is true). Not available for redis or mongodb.",
	}, getDdlTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_query",
		Description: "Run one SQL statement and return rows as JSON. A postgres profile may expose several databases; " +
			"pass database to pick one. On a read-only profile the statement is " +
			"screened first: it must start with SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE, " +
			"must not stack statements, and must not contain write keywords " +
			"(insert, update, delete, drop, truncate, alter, create, call, ...). The SQL is sent to the database " +
			"unmodified; no LIMIT is injected, so use the limit argument to bound the response. " +
			"On a read-write profile any statement runs unscreened.",
	}, runQueryTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_redis",
		Description: "Run one Redis command against a redis profile. On a read-only profile only " +
			"allowlisted read commands (get, mget, hgetall, smembers, scan, ttl, type, info, zrange, " +
			"xrange, geosearch, ...) are permitted; writes, blocking commands, eval, subscribe, and " +
			"config are refused. On a read-write profile any command runs.",
	}, runRedisTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_mongo",
		Description: "Run one MongoDB read operation against a mongodb profile: find (filter, " +
			"projection, sort), aggregate (pipeline), count (filter), or distinct (field, filter). " +
			"Filters and pipelines are MongoDB Extended JSON strings, so {\"$oid\": ...} and " +
			"{\"$date\": ...} work. Aggregation stages that write ($out, $merge) are refused on a " +
			"read-only profile.",
	}, runMongoTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "export_query",
		Description: "Run one SQL statement (screened like run_query; a postgres profile may expose several databases, pick one with database) and stream the results into a csv, json, or " +
			"ndjson file. The format is inferred from the file extension when not given. The " +
			"destination's parent directory must exist; it is not created.",
	}, exportQueryTool)
}
