// Package dataview builds the SQL a Data view runs for one page of a
// Table: a per-Adapter SELECT assembled from Table identity, WHERE text,
// ORDER BY text, page size, and offset, with Adapter-correct identifier
// quoting and paging syntax. It has no Bubble Tea dependency, so the
// terminal UI's query building is unit tested without a UI harness, and it
// does not screen the WHERE/ORDER BY text it is given: the terminal UI
// does that from the Profile's access mode (ADR-0003).
package dataview

import (
	"fmt"
	"strings"
)

// PageQuery is the input that identifies one page of a Table.
type PageQuery struct {
	// Path is the Table identity the Catalog tree already carries: the
	// trailing two segments name the table, exactly like
	// catalog.GetColumns addresses it (schema+table for postgres/mssql/
	// odbc, database+table for mysql/clickhouse). Any leading segments
	// are ignored.
	Path []string
	// Where is inserted into the query as the user typed it, unscreened;
	// empty means no filter.
	Where string
	// OrderBy is inserted into the query as the user typed it,
	// unscreened; empty means the Adapter's own default order, except on
	// mssql/odbc where offset paging requires an ORDER BY, so an empty
	// OrderBy there becomes mssqlDefaultOrder instead.
	OrderBy string
	// PageSize is the number of rows the page holds; must be > 0.
	PageSize int
	// Offset is how many rows precede this page; a negative value is
	// treated as 0.
	Offset int
}

// mssqlDefaultOrder is the ORDER BY clause mssql/odbc get when OrderBy is
// empty: SQL Server's OFFSET/FETCH paging has no unordered form, so a
// clause has to be present either way. This is the same fixed clause every
// time (never random, never dependent on the query), which is the only
// sense in which a caller with no column knowledge can offer a "stable"
// default: it is not a guarantee that concurrent writes can never reshuffle
// the result, only that building the query never fails for lack of an
// ORDER BY.
const mssqlDefaultOrder = "(SELECT NULL)"

// BuildPageQuery renders the SELECT that returns one page of q's Table for
// adapter.
func BuildPageQuery(adapter string, q PageQuery) (string, error) {
	if q.PageSize <= 0 {
		return "", fmt.Errorf("page size must be greater than zero (got %d)", q.PageSize)
	}
	if !supportedAdapter(adapter) {
		return "", fmt.Errorf("page query is not available for %s", adapter)
	}
	table, err := tableRef(adapter, q.Path)
	if err != nil {
		return "", err
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	var b strings.Builder
	b.WriteString("SELECT * FROM ")
	b.WriteString(table)
	if q.Where != "" {
		b.WriteString(" WHERE ")
		b.WriteString(q.Where)
	}
	orderBy := q.OrderBy
	if orderBy == "" && needsOrderForPaging(adapter) {
		orderBy = mssqlDefaultOrder
	}
	if orderBy != "" {
		b.WriteString(" ORDER BY ")
		b.WriteString(orderBy)
	}
	b.WriteString(" ")
	b.WriteString(pagingClause(adapter, q.PageSize, offset))
	return b.String(), nil
}

// BuildCountQuery renders the SELECT COUNT(*) a Data view runs on demand
// for the Table at path filtered by where (inserted unscreened, like
// PageQuery.Where; empty means no filter). Nothing runs it implicitly
// (spec: opening a Table never runs an expensive count).
func BuildCountQuery(adapter string, path []string, where string) (string, error) {
	if !supportedAdapter(adapter) {
		return "", fmt.Errorf("count query is not available for %s", adapter)
	}
	table, err := tableRef(adapter, path)
	if err != nil {
		return "", err
	}
	q := "SELECT COUNT(*) FROM " + table
	if where != "" {
		q += " WHERE " + where
	}
	return q, nil
}

// BuildExportQuery renders the SELECT a Data view exports: the same Table,
// WHERE, and ORDER BY as its page query (inserted unscreened), but with no
// paging clause, so the export is not limited to a page. Without paging,
// mssql/odbc need no default ORDER BY either.
func BuildExportQuery(adapter string, path []string, where, orderBy string) (string, error) {
	if !supportedAdapter(adapter) {
		return "", fmt.Errorf("export query is not available for %s", adapter)
	}
	table, err := tableRef(adapter, path)
	if err != nil {
		return "", err
	}
	q := "SELECT * FROM " + table
	if where != "" {
		q += " WHERE " + where
	}
	if orderBy != "" {
		q += " ORDER BY " + orderBy
	}
	return q, nil
}

// supportedAdapter reports whether adapter is one of the SQL Adapters a
// Data view can page; redis and mongodb address rows differently and never
// reach this builder.
func supportedAdapter(adapter string) bool {
	switch adapter {
	case "postgres", "mysql", "mssql", "odbc", "clickhouse":
		return true
	}
	return false
}

// needsOrderForPaging reports whether adapter's paging syntax requires an
// ORDER BY clause to be present at all (mssql/odbc's OFFSET/FETCH; every
// other Adapter's LIMIT/OFFSET works unordered).
func needsOrderForPaging(adapter string) bool {
	return adapter == "mssql" || adapter == "odbc"
}

// pagingClause renders the paging syntax for adapter.
func pagingClause(adapter string, pageSize, offset int) string {
	if needsOrderForPaging(adapter) {
		return fmt.Sprintf("OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", offset, pageSize)
	}
	return fmt.Sprintf("LIMIT %d OFFSET %d", pageSize, offset)
}

// tableRef renders the quoted table reference from the trailing two
// segments of path, matching catalog.GetColumns's own path addressing.
func tableRef(adapter string, path []string) (string, error) {
	if len(path) < 2 {
		return "", fmt.Errorf("path needs at least 2 segments naming a table, got %q", strings.Join(path, "."))
	}
	parent, table := path[len(path)-2], path[len(path)-1]
	return quoteIdent(adapter, parent) + "." + quoteIdent(adapter, table), nil
}

// quoteIdent quotes name per adapter's identifier syntax, escaping an
// embedded quote character by doubling it.
func quoteIdent(adapter, name string) string {
	switch adapter {
	case "postgres":
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	case "mssql", "odbc":
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	default: // mysql, clickhouse
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
}
