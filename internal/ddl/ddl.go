package ddl

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Snarap1/hqdb/internal/adapters"
)

// Out is the get_ddl response.
type Out struct {
	Table      string `json:"table"`
	Definition string `json:"definition"`
	// Native is true when Definition is the server's own stored text
	// (mysql, clickhouse, and any view); false means Definition was
	// reconstructed from catalog rows (postgres, mssql, odbc tables).
	Native bool `json:"native"`
}

// GetDDL returns the definition of the table or view addressed by path.
// redis and mongodb profiles have no DDL concept and return an error naming
// the adapter, like the other SQL-only tools.
func GetDDL(ctx context.Context, conn adapters.DB, path []string) (*Out, error) {
	sqlConn, ok := conn.(*adapters.SQL)
	if !ok {
		return nil, adapters.ErrNotSQL(conn.Kind(), "get_ddl")
	}
	if len(path) < 2 {
		return nil, fmt.Errorf("path needs at least 2 segments, e.g. %s", ddlUsage(sqlConn.Kind()))
	}
	schema, table := path[len(path)-2], path[len(path)-1]
	switch sqlConn.Kind() {
	case "mysql":
		return showCreate(ctx, sqlConn, fmt.Sprintf("SHOW CREATE TABLE `%s`.`%s`", schema, table), schema, table)
	case "clickhouse":
		return showCreateClickHouse(ctx, sqlConn, schema, table)
	case "postgres":
		return postgresDDL(ctx, sqlConn, schema, table)
	case "mssql", "odbc":
		return mssqlDDL(ctx, sqlConn, schema, table)
	default:
		return nil, fmt.Errorf("get_ddl is not available for %s", sqlConn.Kind())
	}
}

// ddlUsage is the per-adapter path example in the too-short-path error.
func ddlUsage(kind string) string {
	if kind == "postgres" || kind == "mssql" || kind == "odbc" {
		return `["hqdb", "public", "widgets"]`
	}
	return `["hqdb", "widgets"]`
}

// showCreate runs SHOW CREATE TABLE (mysql, for both tables and views) and
// returns its second result column, which carries the definition text in
// either shape ("Table"/"Create Table" or "View"/"Create View"/...).
func showCreate(ctx context.Context, conn *adapters.SQL, query, schema, table string) (*Out, error) {
	rows, err := conn.SQL().QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		return nil, fmt.Errorf("no such table %q.%q", schema, table)
	}
	dest := make([]any, len(cols))
	for i := range dest {
		dest[i] = new(sql.NullString)
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	def, _ := dest[1].(*sql.NullString)
	return &Out{Table: schema + "." + table, Definition: def.String, Native: true}, rows.Err()
}

// showCreateClickHouse runs ClickHouse's own SHOW CREATE TABLE, which
// returns a single "statement" column for both tables and views.
func showCreateClickHouse(ctx context.Context, conn *adapters.SQL, database, table string) (*Out, error) {
	var def string
	err := conn.SQL().QueryRowContext(ctx,
		fmt.Sprintf("SHOW CREATE TABLE %s.%s", quoteIdentBacktick(database), quoteIdentBacktick(table)),
	).Scan(&def)
	if err != nil {
		return nil, err
	}
	return &Out{Table: database + "." + table, Definition: def, Native: true}, nil
}

// quoteIdentBacktick quotes an identifier for clickhouse/mysql.
func quoteIdentBacktick(name string) string { return "`" + name + "`" }

// isView reports whether relkind marks a view or materialized view
// (postgres pg_class.relkind).
func isView(relkind string) bool { return relkind == "v" || relkind == "m" }

// postgresDDL fetches the native view definition, or reconstructs a table's
// DDL from pg_catalog rows.
func postgresDDL(ctx context.Context, conn *adapters.SQL, schema, table string) (*Out, error) {
	db := conn.SQL()
	var oid int64
	var relkind string
	err := db.QueryRowContext(ctx,
		`SELECT c.oid, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace `+
			`WHERE n.nspname = $1 AND c.relname = $2`, schema, table).Scan(&oid, &relkind)
	if err != nil {
		return nil, fmt.Errorf("no such table %q.%q: %w", schema, table, err)
	}
	if isView(relkind) {
		var def string
		if err := db.QueryRowContext(ctx, `SELECT pg_get_viewdef($1::oid, true)`, oid).Scan(&def); err != nil {
			return nil, err
		}
		return &Out{Table: schema + "." + table, Definition: strings.TrimSpace(def), Native: true}, nil
	}

	columns, err := postgresColumns(ctx, db, oid)
	if err != nil {
		return nil, err
	}
	pk, err := postgresPrimaryKey(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	uniques, err := postgresUniques(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	checks, err := postgresChecks(ctx, db, oid)
	if err != nil {
		return nil, err
	}
	indexes, err := postgresIndexes(ctx, db, oid)
	if err != nil {
		return nil, err
	}
	fks, err := postgresForeignKeys(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}

	def := Reconstruct(Table{
		Schema: schema, Name: table, Columns: columns, PrimaryKey: pk,
		ForeignKeys: fks, Indexes: indexes, Checks: checks, Uniques: uniques,
	}, "postgres")
	return &Out{Table: schema + "." + table, Definition: def, Native: false}, nil
}

func postgresColumns(ctx context.Context, db *sql.DB, oid int64) ([]Column, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull, `+
			`COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') `+
			`FROM pg_attribute a `+
			`LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum `+
			`WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped `+
			`ORDER BY a.attnum`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var name, typ, def string
		var notNull bool
		if err := rows.Scan(&name, &typ, &notNull, &def); err != nil {
			return nil, err
		}
		out = append(out, Column{Name: name, Type: typ, Nullable: !notNull, Default: def})
	}
	return out, rows.Err()
}

func postgresPrimaryKey(ctx context.Context, db *sql.DB, schema, table string) (*PrimaryKey, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT tc.constraint_name, kcu.column_name `+
			`FROM information_schema.table_constraints tc `+
			`JOIN information_schema.key_column_usage kcu `+
			`  ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema `+
			`WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'PRIMARY KEY' `+
			`ORDER BY kcu.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pk *PrimaryKey
	for rows.Next() {
		var name, col string
		if err := rows.Scan(&name, &col); err != nil {
			return nil, err
		}
		if pk == nil {
			pk = &PrimaryKey{Name: name}
		}
		pk.Columns = append(pk.Columns, col)
	}
	return pk, rows.Err()
}

func postgresUniques(ctx context.Context, db *sql.DB, schema, table string) ([]UniqueConstraint, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT tc.constraint_name, kcu.column_name `+
			`FROM information_schema.table_constraints tc `+
			`JOIN information_schema.key_column_usage kcu `+
			`  ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema `+
			`WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'UNIQUE' `+
			`ORDER BY tc.constraint_name, kcu.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return groupNamedColumns(rows, func(name string) UniqueConstraint { return UniqueConstraint{Name: name} },
		func(u *UniqueConstraint, col string) { u.Columns = append(u.Columns, col) })
}

func postgresChecks(ctx context.Context, db *sql.DB, oid int64) ([]CheckConstraint, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT conname, pg_get_constraintdef(oid, true) FROM pg_constraint `+
			`WHERE conrelid = $1 AND contype = 'c' ORDER BY conname`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckConstraint
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		out = append(out, CheckConstraint{Name: name, Expr: trimCheckKeyword(def)})
	}
	return out, rows.Err()
}

// trimCheckKeyword strips a leading "CHECK (" / trailing ")" that
// pg_get_constraintdef already includes, since Reconstruct adds its own.
func trimCheckKeyword(def string) string {
	def = strings.TrimSpace(def)
	def = strings.TrimPrefix(def, "CHECK ")
	def = strings.TrimPrefix(def, "(")
	def = strings.TrimSuffix(def, ")")
	return def
}

func postgresIndexes(ctx context.Context, db *sql.DB, oid int64) ([]Index, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ic.relname, a.attname, i.indisunique, array_position(i.indkey, a.attnum) `+
			`FROM pg_index i `+
			`JOIN pg_class ic ON ic.oid = i.indexrelid `+
			`JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey) `+
			`WHERE i.indrelid = $1 AND NOT i.indisprimary `+
			`ORDER BY ic.relname, array_position(i.indkey, a.attnum)`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*Index{}
	var order []string
	for rows.Next() {
		var name, col string
		var unique bool
		var pos int
		if err := rows.Scan(&name, &col, &unique, &pos); err != nil {
			return nil, err
		}
		idx, ok := byName[name]
		if !ok {
			idx = &Index{Name: name, Unique: unique}
			byName[name] = idx
			order = append(order, name)
		}
		idx.Columns = append(idx.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Index, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

func postgresForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT tc.constraint_name, kcu.column_name, kcu.ordinal_position, `+
			`ccu.table_schema, ccu.table_name, ccu.column_name `+
			`FROM information_schema.table_constraints tc `+
			`JOIN information_schema.key_column_usage kcu `+
			`  ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema `+
			`JOIN information_schema.constraint_column_usage ccu `+
			`  ON ccu.constraint_name = tc.constraint_name AND ccu.constraint_schema = tc.constraint_schema `+
			`WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'FOREIGN KEY' `+
			`ORDER BY tc.constraint_name, kcu.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*ForeignKey{}
	var order []string
	for rows.Next() {
		var name, col string
		var pos int
		var refSchema, refTable, refCol string
		if err := rows.Scan(&name, &col, &pos, &refSchema, &refTable, &refCol); err != nil {
			return nil, err
		}
		fk, ok := byName[name]
		if !ok {
			fk = &ForeignKey{Name: name, RefSchema: refSchema, RefTable: refTable}
			byName[name] = fk
			order = append(order, name)
		}
		fk.Columns = append(fk.Columns, col)
		fk.RefColumns = append(fk.RefColumns, refCol)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ForeignKey, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

// groupNamedColumns folds rows shaped (name, column) into one value per
// name, used for postgres unique constraints.
func groupNamedColumns[T any](rows *sql.Rows, newT func(name string) T, add func(*T, string)) ([]T, error) {
	byName := map[string]*T{}
	var order []string
	for rows.Next() {
		var name, col string
		if err := rows.Scan(&name, &col); err != nil {
			return nil, err
		}
		v, ok := byName[name]
		if !ok {
			nv := newT(name)
			v = &nv
			byName[name] = v
			order = append(order, name)
		}
		add(v, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]T, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

// mssqlDDL fetches the native view definition, or reconstructs a table's
// DDL from INFORMATION_SCHEMA / sys rows; it also serves odbc, which speaks
// the same T-SQL catalog.
func mssqlDDL(ctx context.Context, conn *adapters.SQL, schema, table string) (*Out, error) {
	db := conn.SQL()
	var isViewFlag bool
	err := db.QueryRowContext(ctx,
		`SELECT CASE WHEN type = 'V' THEN 1 ELSE 0 END FROM sys.objects o `+
			`JOIN sys.schemas s ON s.schema_id = o.schema_id `+
			`WHERE s.name = ? AND o.name = ? AND o.type IN ('U', 'V')`, schema, table).Scan(&isViewFlag)
	if err != nil {
		return nil, fmt.Errorf("no such table %q.%q: %w", schema, table, err)
	}
	if isViewFlag {
		var def sql.NullString
		err := db.QueryRowContext(ctx,
			`SELECT sm.definition FROM sys.sql_modules sm `+
				`JOIN sys.objects o ON o.object_id = sm.object_id `+
				`JOIN sys.schemas s ON s.schema_id = o.schema_id `+
				`WHERE s.name = ? AND o.name = ?`, schema, table).Scan(&def)
		if err != nil {
			return nil, err
		}
		return &Out{Table: schema + "." + table, Definition: strings.TrimSpace(def.String), Native: true}, nil
	}

	columns, err := mssqlColumns(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	pk, err := mssqlKeyConstraint(ctx, db, schema, table, "PRIMARY KEY")
	if err != nil {
		return nil, err
	}
	var primaryKey *PrimaryKey
	if pk != nil {
		primaryKey = &PrimaryKey{Name: pk.Name, Columns: pk.Columns}
	}
	uniques, err := mssqlUniques(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	checks, err := mssqlChecks(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	indexes, err := mssqlIndexes(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	fks, err := mssqlForeignKeys(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}

	def := Reconstruct(Table{
		Schema: schema, Name: table, Columns: columns, PrimaryKey: primaryKey,
		ForeignKeys: fks, Indexes: indexes, Checks: checks, Uniques: uniques,
	}, "mssql")
	return &Out{Table: schema + "." + table, Definition: def, Native: false}, nil
}

func mssqlColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, `+
			`COALESCE(CHARACTER_MAXIMUM_LENGTH, NUMERIC_PRECISION), NUMERIC_SCALE, `+
			`COALESCE(COLUMN_DEFAULT, '') `+
			`FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? `+
			`ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var name, dataType, nullable, def string
		var size sql.NullInt64
		var scale sql.NullInt64
		if err := rows.Scan(&name, &dataType, &nullable, &size, &scale, &def); err != nil {
			return nil, err
		}
		typ := dataType
		if size.Valid && needsSize(dataType) {
			if scale.Valid && scale.Int64 > 0 {
				typ = fmt.Sprintf("%s(%d,%d)", dataType, size.Int64, scale.Int64)
			} else {
				typ = fmt.Sprintf("%s(%d)", dataType, size.Int64)
			}
		}
		out = append(out, Column{Name: name, Type: typ, Nullable: nullable == "YES", Default: trimParens(def)})
	}
	return out, rows.Err()
}

// needsSize reports whether an mssql data type is conventionally shown with
// a length/precision suffix.
func needsSize(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "varchar", "nvarchar", "char", "nchar", "decimal", "numeric":
		return true
	default:
		return false
	}
}

// trimParens strips the single wrapping parens mssql's COLUMN_DEFAULT
// carries, e.g. "((0))" -> "0".
func trimParens(s string) string {
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = s[1 : len(s)-1]
	}
	return s
}

type namedColumns struct {
	Name    string
	Columns []string
}

func mssqlKeyConstraint(ctx context.Context, db *sql.DB, schema, table, constraintType string) (*namedColumns, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT tc.CONSTRAINT_NAME, kcu.COLUMN_NAME `+
			`FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc `+
			`JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu `+
			`  ON kcu.CONSTRAINT_NAME = tc.CONSTRAINT_NAME AND kcu.TABLE_SCHEMA = tc.TABLE_SCHEMA `+
			`WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ? AND tc.CONSTRAINT_TYPE = ? `+
			`ORDER BY kcu.ORDINAL_POSITION`, schema, table, constraintType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out *namedColumns
	for rows.Next() {
		var name, col string
		if err := rows.Scan(&name, &col); err != nil {
			return nil, err
		}
		if out == nil {
			out = &namedColumns{Name: name}
		}
		out.Columns = append(out.Columns, col)
	}
	return out, rows.Err()
}

func mssqlUniques(ctx context.Context, db *sql.DB, schema, table string) ([]UniqueConstraint, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT tc.CONSTRAINT_NAME, kcu.COLUMN_NAME `+
			`FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc `+
			`JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu `+
			`  ON kcu.CONSTRAINT_NAME = tc.CONSTRAINT_NAME AND kcu.TABLE_SCHEMA = tc.TABLE_SCHEMA `+
			`WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ? AND tc.CONSTRAINT_TYPE = 'UNIQUE' `+
			`ORDER BY tc.CONSTRAINT_NAME, kcu.ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return groupNamedColumns(rows, func(name string) UniqueConstraint { return UniqueConstraint{Name: name} },
		func(u *UniqueConstraint, col string) { u.Columns = append(u.Columns, col) })
}

func mssqlChecks(ctx context.Context, db *sql.DB, schema, table string) ([]CheckConstraint, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT cc.CONSTRAINT_NAME, cc.CHECK_CLAUSE `+
			`FROM INFORMATION_SCHEMA.CHECK_CONSTRAINTS cc `+
			`JOIN INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc `+
			`  ON tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME AND tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA `+
			`WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ? ORDER BY cc.CONSTRAINT_NAME`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckConstraint
	for rows.Next() {
		var name, expr string
		if err := rows.Scan(&name, &expr); err != nil {
			return nil, err
		}
		out = append(out, CheckConstraint{Name: name, Expr: trimParens(expr)})
	}
	return out, rows.Err()
}

func mssqlIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT i.name, c.name, i.is_unique, ic.key_ordinal `+
			`FROM sys.indexes i `+
			`JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id `+
			`JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id `+
			`JOIN sys.objects o ON o.object_id = i.object_id `+
			`JOIN sys.schemas s ON s.schema_id = o.schema_id `+
			`WHERE s.name = ? AND o.name = ? AND i.is_primary_key = 0 AND i.name IS NOT NULL `+
			`ORDER BY i.name, ic.key_ordinal`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*Index{}
	var order []string
	for rows.Next() {
		var name, col string
		var unique bool
		var pos int
		if err := rows.Scan(&name, &col, &unique, &pos); err != nil {
			return nil, err
		}
		idx, ok := byName[name]
		if !ok {
			idx = &Index{Name: name, Unique: unique}
			byName[name] = idx
			order = append(order, name)
		}
		idx.Columns = append(idx.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Index, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

func mssqlForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT fk.name, c.name, fc.constraint_column_id, rs.name, rt.name, rc.name `+
			`FROM sys.foreign_keys fk `+
			`JOIN sys.foreign_key_columns fc ON fc.constraint_object_id = fk.object_id `+
			`JOIN sys.objects o ON o.object_id = fk.parent_object_id `+
			`JOIN sys.schemas s ON s.schema_id = o.schema_id `+
			`JOIN sys.columns c ON c.object_id = fc.parent_object_id AND c.column_id = fc.parent_column_id `+
			`JOIN sys.objects ro ON ro.object_id = fk.referenced_object_id `+
			`JOIN sys.schemas rs ON rs.schema_id = ro.schema_id `+
			`JOIN sys.tables rt ON rt.object_id = ro.object_id `+
			`JOIN sys.columns rc ON rc.object_id = fc.referenced_object_id AND rc.column_id = fc.referenced_column_id `+
			`WHERE s.name = ? AND o.name = ? `+
			`ORDER BY fk.name, fc.constraint_column_id`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*ForeignKey{}
	var order []string
	for rows.Next() {
		var name, col string
		var pos int
		var refSchema, refTable, refCol string
		if err := rows.Scan(&name, &col, &pos, &refSchema, &refTable, &refCol); err != nil {
			return nil, err
		}
		fk, ok := byName[name]
		if !ok {
			fk = &ForeignKey{Name: name, RefSchema: refSchema, RefTable: refTable}
			byName[name] = fk
			order = append(order, name)
		}
		fk.Columns = append(fk.Columns, col)
		fk.RefColumns = append(fk.RefColumns, refCol)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ForeignKey, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}
