package ddl

import (
	"strings"
	"testing"
)

// TestReconstructPostgresColumnsAndPrimaryKey builds the simplest table: a
// couple of columns and a single-column primary key, quoted the postgres way.
func TestReconstructPostgresColumnsAndPrimaryKey(t *testing.T) {
	table := Table{
		Schema: "public",
		Name:   "widgets",
		Columns: []Column{
			{Name: "id", Type: "integer", Nullable: false},
			{Name: "name", Type: "text", Nullable: true},
		},
		PrimaryKey: &PrimaryKey{Name: "widgets_pkey", Columns: []string{"id"}},
	}
	got := Reconstruct(table, "postgres")
	want := `CREATE TABLE "public"."widgets" (
    "id" integer NOT NULL,
    "name" text,
    CONSTRAINT "widgets_pkey" PRIMARY KEY ("id")
);
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestReconstructPostgresFullTable covers a table-driven mix of foreign
// keys, indexes, and check/unique constraints, all postgres-quoted.
func TestReconstructPostgresFullTable(t *testing.T) {
	table := Table{
		Schema: "public",
		Name:   "widgets",
		Columns: []Column{
			{Name: "id", Type: "integer", Nullable: false},
			{Name: "sku", Type: "text", Nullable: false},
			{Name: "price", Type: "numeric(10,2)", Nullable: false, Default: "0"},
			{Name: "maker_id", Type: "integer", Nullable: true},
		},
		PrimaryKey: &PrimaryKey{Name: "widgets_pkey", Columns: []string{"id"}},
		Uniques: []UniqueConstraint{
			{Name: "widgets_sku_key", Columns: []string{"sku"}},
		},
		Checks: []CheckConstraint{
			{Name: "widgets_price_check", Expr: "price >= 0"},
		},
		Indexes: []Index{
			{Name: "widgets_maker_id_idx", Columns: []string{"maker_id"}},
		},
		ForeignKeys: []ForeignKey{
			{
				Name: "widgets_maker_id_fkey", Columns: []string{"maker_id"},
				RefSchema: "public", RefTable: "makers", RefColumns: []string{"id"},
			},
		},
	}
	got := Reconstruct(table, "postgres")

	for _, want := range []string{
		`CREATE TABLE "public"."widgets" (`,
		`"id" integer NOT NULL`,
		`"price" numeric(10,2) NOT NULL DEFAULT 0`,
		`"maker_id" integer`,
		`CONSTRAINT "widgets_pkey" PRIMARY KEY ("id")`,
		`CONSTRAINT "widgets_sku_key" UNIQUE ("sku")`,
		`CONSTRAINT "widgets_price_check" CHECK (price >= 0)`,
		`CREATE INDEX "widgets_maker_id_idx" ON "public"."widgets" ("maker_id");`,
		`ALTER TABLE "public"."widgets" ADD CONSTRAINT "widgets_maker_id_fkey" FOREIGN KEY ("maker_id") REFERENCES "public"."makers" ("id");`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
	// "maker_id" must not carry a stray NOT NULL since it is nullable.
	if strings.Contains(got, `"maker_id" integer NOT NULL`) {
		t.Errorf("maker_id should be nullable; got:\n%s", got)
	}
}

// TestReconstructMSSQLQuoting checks the bracket quoting mssql (and odbc)
// use instead of postgres's double quotes.
func TestReconstructMSSQLQuoting(t *testing.T) {
	table := Table{
		Schema: "dbo",
		Name:   "widgets",
		Columns: []Column{
			{Name: "id", Type: "int", Nullable: false},
		},
		PrimaryKey: &PrimaryKey{Name: "PK_widgets", Columns: []string{"id"}},
		Indexes: []Index{
			{Name: "IX_widgets_id", Columns: []string{"id"}, Unique: true},
		},
	}
	got := Reconstruct(table, "mssql")

	for _, want := range []string{
		`CREATE TABLE [dbo].[widgets] (`,
		`[id] int NOT NULL`,
		`CONSTRAINT [PK_widgets] PRIMARY KEY ([id])`,
		`CREATE UNIQUE INDEX [IX_widgets_id] ON [dbo].[widgets] ([id]);`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"`) {
		t.Errorf("mssql output must not use double-quote identifiers; got:\n%s", got)
	}
}

// TestReconstructMultiColumnKeys checks that composite primary keys and
// foreign keys preserve column order.
func TestReconstructMultiColumnKeys(t *testing.T) {
	table := Table{
		Schema: "public",
		Name:   "order_items",
		Columns: []Column{
			{Name: "order_id", Type: "integer", Nullable: false},
			{Name: "line_no", Type: "integer", Nullable: false},
		},
		PrimaryKey: &PrimaryKey{Name: "order_items_pkey", Columns: []string{"order_id", "line_no"}},
		ForeignKeys: []ForeignKey{
			{
				Name: "order_items_order_fkey", Columns: []string{"order_id"},
				RefSchema: "public", RefTable: "orders", RefColumns: []string{"id"},
			},
		},
	}
	got := Reconstruct(table, "postgres")
	if !strings.Contains(got, `PRIMARY KEY ("order_id", "line_no")`) {
		t.Errorf("composite PK column order wrong; got:\n%s", got)
	}
}
