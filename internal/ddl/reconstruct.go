// Package ddl reconstructs a table's definition from system-catalog rows for
// adapters that have no native "show me the DDL" command, and fetches the
// native definition where one exists.
package ddl

import "strings"

// Column is one reconstructed column.
type Column struct {
	Name     string
	Type     string
	Nullable bool
	// Default is the column's default expression, verbatim from the
	// catalog; empty means no default.
	Default string
}

// PrimaryKey is a table's primary key constraint.
type PrimaryKey struct {
	Name    string
	Columns []string
}

// ForeignKey is one foreign key constraint.
type ForeignKey struct {
	Name       string
	Columns    []string
	RefSchema  string
	RefTable   string
	RefColumns []string
}

// Index is one non-primary-key index.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
}

// CheckConstraint is one CHECK constraint; Expr is the boolean expression
// without the surrounding "CHECK (...)".
type CheckConstraint struct {
	Name string
	Expr string
}

// UniqueConstraint is one UNIQUE constraint (distinct from a unique index).
type UniqueConstraint struct {
	Name    string
	Columns []string
}

// Table is everything Reconstruct needs to build a CREATE TABLE statement
// and its accompanying indexes and foreign keys.
type Table struct {
	Schema      string
	Name        string
	Columns     []Column
	PrimaryKey  *PrimaryKey
	ForeignKeys []ForeignKey
	Indexes     []Index
	Checks      []CheckConstraint
	Uniques     []UniqueConstraint
}

// Reconstruct builds a reconstructed DDL text for table in the given dialect
// ("postgres" or "mssql"; mssql quoting also covers odbc). It is a pure
// function over catalog rows: no network, no randomness.
func Reconstruct(table Table, dialect string) string {
	q := quoter(dialect)
	var sb strings.Builder

	sb.WriteString("CREATE TABLE ")
	sb.WriteString(q(table.Schema) + "." + q(table.Name))
	sb.WriteString(" (\n")

	var lines []string
	for _, c := range table.Columns {
		line := "    " + q(c.Name) + " " + c.Type
		if !c.Nullable {
			line += " NOT NULL"
		}
		if c.Default != "" {
			line += " DEFAULT " + c.Default
		}
		lines = append(lines, line)
	}
	if table.PrimaryKey != nil {
		lines = append(lines, "    CONSTRAINT "+q(table.PrimaryKey.Name)+" PRIMARY KEY ("+quoteList(table.PrimaryKey.Columns, q)+")")
	}
	for _, u := range table.Uniques {
		lines = append(lines, "    CONSTRAINT "+q(u.Name)+" UNIQUE ("+quoteList(u.Columns, q)+")")
	}
	for _, c := range table.Checks {
		lines = append(lines, "    CONSTRAINT "+q(c.Name)+" CHECK ("+c.Expr+")")
	}
	sb.WriteString(strings.Join(lines, ",\n"))
	sb.WriteString("\n);\n")

	for _, idx := range table.Indexes {
		sb.WriteString("\n")
		if idx.Unique {
			sb.WriteString("CREATE UNIQUE INDEX ")
		} else {
			sb.WriteString("CREATE INDEX ")
		}
		sb.WriteString(q(idx.Name) + " ON " + q(table.Schema) + "." + q(table.Name))
		sb.WriteString(" (" + quoteList(idx.Columns, q) + ");\n")
	}

	for _, fk := range table.ForeignKeys {
		sb.WriteString("\n")
		sb.WriteString("ALTER TABLE " + q(table.Schema) + "." + q(table.Name) +
			" ADD CONSTRAINT " + q(fk.Name) + " FOREIGN KEY (" + quoteList(fk.Columns, q) + ")" +
			" REFERENCES " + q(fk.RefSchema) + "." + q(fk.RefTable) + " (" + quoteList(fk.RefColumns, q) + ");\n")
	}

	return sb.String()
}

// quoter returns the identifier-quoting function for dialect.
func quoter(dialect string) func(string) string {
	if dialect == "mssql" {
		return func(name string) string { return "[" + name + "]" }
	}
	return func(name string) string { return `"` + name + `"` }
}

// quoteList quotes and comma-joins a column list.
func quoteList(names []string, q func(string) string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = q(n)
	}
	return strings.Join(quoted, ", ")
}
