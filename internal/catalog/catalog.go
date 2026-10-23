// Package catalog lists database objects one level at a time: relational
// databases, schemas, and tables, or redis key namespaces.
package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// Limits parameterizes one GetSchema call.
// NodeCap bounds how many nodes a level returns; zero or negative means
// unlimited.
// CharCap bounds the marshaled payload size; zero or negative means no
// payload cap.
// Offset skips that many nodes from the level's stable order, letting a
// caller fetch the next chunk of a large level.
type Limits struct {
	NodeCap int
	CharCap int
	Offset  int
}

// DefaultLimits are the caps the MCP get_schema tool has always used.
var DefaultLimits = Limits{NodeCap: 500, CharCap: 60000}

// Redis schema scanning limits.
const (
	redisScanCount   = 500
	redisMaxKeys     = 10000
	redisTypeSamples = 25
)

// SchemaNode is one entry of a get_schema level.
type SchemaNode struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	Type  string `json:"type,omitempty"`
}

// Column describes one column of a table.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable string `json:"nullable"`
}

// SchemaOut is the get_schema response. Truncated is set when the node cap
// cut the level short, whether that is the first chunk of a large level or
// a chunk fetched with an offset that is not the last one.
type SchemaOut struct {
	Nodes     []SchemaNode `json:"nodes"`
	Columns   []Column     `json:"columns,omitempty"`
	Truncated bool         `json:"truncated"`
}

// ColumnsOut is the get_columns response.
type ColumnsOut struct {
	Table   string   `json:"table"`
	Columns []Column `json:"columns"`
}

const sharedTablesQuery = `SELECT table_name, table_type FROM information_schema.tables ` +
	`WHERE table_schema = ? AND lower(table_name) LIKE lower(?) ORDER BY table_name`

const columnsQuery = `SELECT column_name, data_type, is_nullable FROM information_schema.columns ` +
	`WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`

// systemSchemas are the built-in schemas hidden from get_schema.
var systemSchemas = map[string][]string{
	"postgres": {"pg_catalog", "information_schema", "pg_toast"},
	"mysql":    {"mysql", "information_schema", "performance_schema", "sys"},
	"mssql": {
		"sys", "INFORMATION_SCHEMA", "guest",
		// The fixed-role schemas SQL Server creates in every database.
		"db_accessadmin", "db_backupoperator", "db_datareader", "db_datawriter",
		"db_ddladmin", "db_denydatareader", "db_denydatawriter", "db_owner", "db_securityadmin",
	},
}

// GetSchema returns the children of the level addressed by path, paginated
// per limits.
func GetSchema(ctx context.Context, conn adapters.DB, path []string, nameFilter string, includeColumns bool, limits Limits) (*SchemaOut, error) {
	var (
		nodes []SchemaNode
		err   error
	)
	switch c := conn.(type) {
	case *adapters.Redis:
		nodes, err = redisSchemaNodes(ctx, c, path, nameFilter)
	case *adapters.SQL:
		nodes, err = sqlSchemaNodes(ctx, c, path, nameFilter)
	case *adapters.Mongo:
		nodes, err = mongoSchemaNodes(ctx, c, path, nameFilter)
	default:
		return nil, fmt.Errorf("get_schema is not available for %s", conn.Kind())
	}
	if err != nil {
		return nil, err
	}
	nodes, more := paginate(nodes, limits)
	if includeColumns {
		if redisConn, ok := conn.(*adapters.Redis); ok {
			if err := annotateRedisTypes(ctx, redisConn, path, nodes); err != nil {
				return nil, err
			}
		}
	}
	out := &SchemaOut{Nodes: nodes, Truncated: more}
	if overCap(out, limits) {
		return nil, fmt.Errorf("result too large; drill down using path")
	}
	return out, nil
}

// paginate applies limits' offset and node cap to a stably ordered node
// list, returning the nodes for this chunk and whether more nodes remain
// beyond it.
func paginate(nodes []SchemaNode, limits Limits) ([]SchemaNode, bool) {
	offset := limits.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > len(nodes) {
		offset = len(nodes)
	}
	nodes = nodes[offset:]
	if limits.NodeCap > 0 && len(nodes) > limits.NodeCap {
		return nodes[:limits.NodeCap], true
	}
	return nodes, false
}

// overCap reports whether out's marshaled size exceeds limits.CharCap; a
// CharCap of zero or less means no payload cap.
func overCap(out *SchemaOut, limits Limits) bool {
	if limits.CharCap <= 0 {
		return false
	}
	b, err := json.Marshal(out)
	return err == nil && len(b) > limits.CharCap
}

// sqlSchemaNodes dispatches the per-adapter catalog queries.
func sqlSchemaNodes(ctx context.Context, conn *adapters.SQL, path []string, nameFilter string) ([]SchemaNode, error) {
	switch conn.Kind() {
	case "postgres", "mysql", "mssql":
		return relationalSchema(ctx, conn, path, nameFilter)
	case "clickhouse":
		return clickhouseSchema(ctx, conn, path, nameFilter)
	default:
		return nil, fmt.Errorf("get_schema is not available for %s", conn.Kind())
	}
}

// rebindPlaceholders rewrites '?' placeholders into the form the driver
// expects: pgx wants $1, $2, ... while mysql, mssql, and clickhouse accept '?'.
func rebindPlaceholders(query string, kind string) string {
	if kind != "postgres" {
		return query
	}
	var sb strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			sb.WriteString("$" + strconv.Itoa(n))
			continue
		}
		sb.WriteByte(query[i])
	}
	return sb.String()
}

// relationalSchema walks databases, then schemas, then tables.
func relationalSchema(ctx context.Context, conn *adapters.SQL, path []string, nameFilter string) ([]SchemaNode, error) {
	kind := conn.Kind()
	if kind == "postgres" && conn.Profile() != nil {
		return postgresSchema(ctx, conn, path, nameFilter)
	}
	return relationalSchemaIn(ctx, conn, path, nameFilter)
}

// postgresSchema lists the Profile's discovered Databases at the top level
// (ADR-0004) and runs every deeper level on the named Database's own
// connection, since a postgres connection is bound to one Database.
func postgresSchema(ctx context.Context, conn *adapters.SQL, path []string, nameFilter string) ([]SchemaNode, error) {
	if len(path) == 0 {
		dbs, err := adapters.Databases(ctx, conn.Profile())
		if err != nil {
			return nil, err
		}
		nodes := make([]SchemaNode, len(dbs))
		for i, d := range dbs {
			nodes[i] = SchemaNode{Name: d, Kind: "database"}
		}
		return nodes, nil
	}
	target, closeTarget, err := ConnForDatabase(ctx, conn, path[0])
	if err != nil {
		return nil, err
	}
	defer closeTarget()
	return relationalSchemaIn(ctx, target, path, nameFilter)
}

// ConnForDatabase returns a connection bound to database: conn itself when
// it already is, otherwise a fresh connection opened with
// adapters.OpenDatabase (which refuses a Database the Profile does not
// expose, listing the available ones). The returned func releases whatever
// was opened and is always safe to call. Adapters that do not discover
// Databases get conn back unchanged.
func ConnForDatabase(ctx context.Context, conn *adapters.SQL, database string) (*adapters.SQL, func(), error) {
	noop := func() {}
	if database == "" || conn.Kind() != "postgres" || conn.Profile() == nil {
		return conn, noop, nil
	}
	var current string
	if err := conn.SQL().QueryRowContext(ctx, `SELECT current_database()`).Scan(&current); err == nil && current == database {
		return conn, noop, nil
	}
	other, err := adapters.OpenDatabase(ctx, conn.Profile(), database)
	if err != nil {
		return nil, noop, err
	}
	return other.(*adapters.SQL), other.Close, nil
}

// relationalSchemaIn is relationalSchema on a connection already bound to
// the Database the path names (for postgres and mssql, which serve one
// Database per connection).
func relationalSchemaIn(ctx context.Context, conn *adapters.SQL, path []string, nameFilter string) ([]SchemaNode, error) {
	kind := conn.Kind()
	tables := func(schema string) ([]SchemaNode, error) {
		rows, err := conn.SQL().QueryContext(ctx, rebindPlaceholders(sharedTablesQuery, kind), schema, likeFilter(nameFilter))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanTables(rows)
	}
	if kind == "mysql" {
		// mysql has no schema level: path is [database] then [database, table].
		switch len(path) {
		case 0:
			excluded := systemSchemas[kind]
			return scanOneColumn(ctx, conn, `SELECT schema_name FROM information_schema.schemata `+
				`WHERE schema_name NOT IN (`+placeholders(len(excluded))+`) ORDER BY schema_name`,
				"database", toArgs(excluded)...)
		case 1:
			return tables(path[0])
		default:
			return nil, fmt.Errorf("path %q is too deep for mysql; expected [database] or [database, table]",
				strings.Join(path, "."))
		}
	}
	// postgres and mssql serve one database per connection.
	switch len(path) {
	case 0:
		q := `SELECT current_database()`
		if kind == "mssql" {
			q = `SELECT DB_NAME()`
		}
		var name string
		if err := conn.SQL().QueryRowContext(ctx, q).Scan(&name); err != nil {
			return nil, err
		}
		return []SchemaNode{{Name: name, Kind: "database"}}, nil
	case 1:
		excluded := systemSchemas[kind]
		q := `SELECT schema_name FROM information_schema.schemata ` +
			`WHERE schema_name NOT IN (` + placeholders(len(excluded)) + `)`
		if kind == "postgres" {
			// postgres reserves the pg_ prefix for system schemas, including
			// the per-session pg_temp_N / pg_toast_temp_N schemas that linger
			// in the catalog once any session has used a temporary table.
			q += ` AND schema_name NOT LIKE 'pg\_%'`
		}
		return scanOneColumn(ctx, conn, q+` ORDER BY schema_name`, "schema", toArgs(excluded)...)
	case 2:
		return tables(path[1])
	default:
		return nil, fmt.Errorf("path %q is too deep for %s; expected [database], [database, schema], or [database, schema, table]",
			strings.Join(path, "."), kind)
	}
}

// clickhouseSchema walks databases, then tables (ClickHouse has no schemas).
func clickhouseSchema(ctx context.Context, conn *adapters.SQL, path []string, nameFilter string) ([]SchemaNode, error) {
	switch len(path) {
	case 0:
		return scanOneColumn(ctx, conn, `SELECT name FROM system.databases ORDER BY name`, "database")
	case 1:
		rows, err := conn.SQL().QueryContext(ctx,
			`SELECT name, engine FROM system.tables WHERE database = ? AND positionCaseInsensitive(name, ?) > 0 ORDER BY name`,
			path[0], nameFilter)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanTables(rows)
	default:
		return nil, fmt.Errorf("path %q is too deep for clickhouse; expected [database] or [database, table]",
			strings.Join(path, "."))
	}
}

// scanOneColumn reads a one-column result set into nodes of the given kind.
func scanOneColumn(ctx context.Context, conn *adapters.SQL, query string, kind string, args ...any) ([]SchemaNode, error) {
	rows, err := conn.SQL().QueryContext(ctx, rebindPlaceholders(query, conn.Kind()), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []SchemaNode
	for rows.Next() {
		var name sql.NullString
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !name.Valid {
			continue
		}
		nodes = append(nodes, SchemaNode{Name: name.String, Kind: kind})
	}
	return nodes, rows.Err()
}

// scanTables reads a name/table_type (or name/engine) result set into nodes.
func scanTables(rows *sql.Rows) ([]SchemaNode, error) {
	var nodes []SchemaNode
	for rows.Next() {
		var name, detail string
		if err := rows.Scan(&name, &detail); err != nil {
			return nil, err
		}
		kind := "table"
		if strings.Contains(strings.ToUpper(detail), "VIEW") {
			kind = "view"
		}
		nodes = append(nodes, SchemaNode{Name: name, Kind: kind})
	}
	return nodes, rows.Err()
}

// likeFilter turns a name filter into a LIKE pattern.
func likeFilter(nameFilter string) string {
	if nameFilter == "" {
		return "%%"
	}
	return "%" + nameFilter + "%"
}

// placeholders builds "?, ?, ..." for an IN list.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// toArgs adapts strings for a variadic query argument list.
func toArgs(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

// GetColumns returns the columns of the table addressed by the trailing two
// path segments.
func GetColumns(ctx context.Context, conn adapters.DB, path []string) (*ColumnsOut, error) {
	if conn.Kind() == "redis" {
		return nil, fmt.Errorf("get_columns is not available for redis; use get_schema")
	}
	if mongoConn, ok := conn.(*adapters.Mongo); ok {
		if len(path) < 2 {
			return nil, fmt.Errorf("path needs at least 2 segments, e.g. %s", columnsUsage("mongodb"))
		}
		return mongoColumns(ctx, mongoConn, path[len(path)-2], path[len(path)-1])
	}
	sqlConn, ok := conn.(*adapters.SQL)
	if !ok {
		return nil, fmt.Errorf("get_columns is not available for %s", conn.Kind())
	}
	if len(path) < 2 {
		return nil, fmt.Errorf("path needs at least 2 segments, e.g. %s", columnsUsage(sqlConn.Kind()))
	}
	schema, table := path[len(path)-2], path[len(path)-1]
	out := &ColumnsOut{Table: schema + "." + table}
	if sqlConn.Kind() == "clickhouse" {
		rows, err := sqlConn.SQL().QueryContext(ctx,
			`SELECT name, type FROM system.columns WHERE database = ? AND table = ? ORDER BY position`,
			schema, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var name, typ string
			if err := rows.Scan(&name, &typ); err != nil {
				return nil, err
			}
			nullable := "NO"
			if strings.Contains(typ, "Nullable(") {
				nullable = "YES"
			}
			out.Columns = append(out.Columns, Column{Name: name, Type: typ, Nullable: nullable})
		}
		return out, rows.Err()
	}
	rows, err := sqlConn.SQL().QueryContext(ctx, rebindPlaceholders(columnsQuery, sqlConn.Kind()), schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ, nullable string
		if err := rows.Scan(&name, &typ, &nullable); err != nil {
			return nil, err
		}
		out.Columns = append(out.Columns, Column{Name: name, Type: typ, Nullable: nullable})
	}
	return out, rows.Err()
}

// columnsUsage is the per-adapter path example in the too-short-path error.
func columnsUsage(kind string) string {
	if kind == "postgres" || kind == "mssql" {
		return `["hqdb", "public", "widgets"]`
	}
	return `["hqdb", "widgets"]`
}

// redisSchemaNodes groups SCAN results into namespace and key nodes by the
// segment following the path prefix.
func redisSchemaNodes(ctx context.Context, conn *adapters.Redis, path []string, nameFilter string) ([]SchemaNode, error) {
	sep := conn.Sep()
	prefix := strings.Join(path, sep)
	prefixWithSep := prefix
	if len(path) > 0 {
		prefixWithSep = prefix + sep
	}
	pattern := "*"
	if len(path) > 0 {
		pattern = prefixWithSep + "*"
	}
	if nameFilter != "" {
		pattern = "*" + nameFilter + "*"
		if len(path) > 0 {
			pattern = prefixWithSep + "*" + nameFilter + "*"
		}
	}
	keys, err := redisScanKeys(ctx, conn, pattern)
	if err != nil {
		return nil, err
	}
	type group struct {
		count int
		key   bool
	}
	groups := map[string]*group{}
	for _, k := range keys {
		rest := k
		if len(path) > 0 {
			if !strings.HasPrefix(k, prefixWithSep) {
				continue
			}
			rest = k[len(prefixWithSep):]
		}
		name, _, more := strings.Cut(rest, sep)
		if name == "" {
			continue
		}
		g, ok := groups[name]
		if !ok {
			g = &group{}
			groups[name] = g
		}
		g.count++
		g.key = g.key || !more
	}
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	nodes := make([]SchemaNode, 0, len(names))
	for _, n := range names {
		g := groups[n]
		kind := "namespace"
		if g.key {
			kind = "key"
		}
		nodes = append(nodes, SchemaNode{Name: n, Kind: kind, Count: g.count})
	}
	return nodes, nil
}

// annotateRedisTypes samples the redis type of up to redisTypeSamples "key"
// kind nodes, run after pagination so the sample lands on the chunk a
// caller actually gets back.
func annotateRedisTypes(ctx context.Context, conn *adapters.Redis, path []string, nodes []SchemaNode) error {
	sep := conn.Sep()
	prefixWithSep := strings.Join(path, sep)
	if len(path) > 0 {
		prefixWithSep += sep
	}
	sampled := 0
	for i := range nodes {
		if nodes[i].Kind != "key" {
			continue
		}
		if sampled >= redisTypeSamples {
			break
		}
		typ, err := conn.Redis().Type(ctx, prefixWithSep+nodes[i].Name).Result()
		if err != nil {
			return err
		}
		nodes[i].Type = typ
		sampled++
	}
	return nil
}

// redisScanKeys iterates SCAN until the cursor returns to 0 or the key cap is hit.
func redisScanKeys(ctx context.Context, conn *adapters.Redis, pattern string) ([]string, error) {
	var (
		keys   []string
		cursor uint64
	)
	for {
		batch, next, err := conn.Redis().Scan(ctx, cursor, pattern, redisScanCount).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 || len(keys) >= redisMaxKeys {
			return keys, nil
		}
	}
}
