package readonly

import "testing"

func TestViolation(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		// want is the expected refusal reason, empty meaning allowed.
		want string
	}{
		{"select", "SELECT 1", ""},
		{"select lowercase", "select id from widgets", ""},
		{"delete refused", "DELETE FROM widgets", "statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE queries are allowed"},
		{"update refused", "UPDATE widgets SET price = 1", "statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE queries are allowed"},
		{"insert refused", "INSERT INTO widgets VALUES (1)", "statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE queries are allowed"},
		{"modifying cte with update refused", "WITH x AS (UPDATE widgets SET price = 1 RETURNING *) SELECT * FROM x", "statement contains write keyword(s): update"},
		{"modifying cte with insert refused", "WITH x AS (INSERT INTO widgets VALUES (9) RETURNING *) SELECT * FROM x", "statement contains write keyword(s): insert"},
		{"select for update refused", "SELECT * FROM widgets FOR UPDATE", "statement contains write keyword(s): update"},
		{"column named like a keyword allowed", "SELECT updated_at, inserted_by FROM widgets", ""},
		{"create refused", "CREATE TABLE x (a int)", "statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE queries are allowed"},
		{"modifying cte refused", "WITH x AS (DELETE FROM widgets RETURNING *) SELECT * FROM x", "statement contains write keyword(s): delete"},
		{"stacked statements refused", "SELECT 1; DROP TABLE widgets", "multiple statements are not allowed; send one read-only query"},
		{"write keyword under select refused", "SELECT * FROM widgets WHERE 1 = 1 -- nothing", ""},
		{"string containing keyword allowed", "SELECT 'this string contains delete' FROM widgets", ""},
		{"line comment containing keyword allowed", "SELECT 1 -- delete", ""},
		{"block comment containing keyword allowed", "SELECT 1 /* drop */", ""},
		{"dollar quote containing keyword allowed", "SELECT $$ drop $$ FROM widgets", ""},
		{"double quoted identifier allowed", `SELECT "delete" FROM widgets`, ""},
		{"explain allowed", "EXPLAIN SELECT * FROM widgets", ""},
		{"write keyword under select refused", "SELECT a, b FROM t WHERE create = 1 AND truncate = 2", "statement contains write keyword(s): create, truncate"},
		{"only semicolons refused", ";;;", "empty statement"},
		{"parenthesized select allowed", "(SELECT 1)", ""},
		{"trailing semicolon is not a second statement", "SELECT 1; ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Violation(tc.sql); got != tc.want {
				t.Errorf("Violation(%q) = %q, want %q", tc.sql, got, tc.want)
			}
		})
	}
}

func TestViolationSortsKeywords(t *testing.T) {
	// Several write keywords in one statement are reported sorted and deduped.
	got := Violation("SELECT drop, drop, create FROM t WHERE x = truncate")
	want := "statement contains write keyword(s): create, drop, truncate"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripSQL(t *testing.T) {
	// Literals and comments are blanked so keyword scanning sees only SQL.
	got := stripSQL("SELECT 'drop' -- drop\n/* delete */ FROM t")
	if want := "SELECT    \n  FROM t"; got != want {
		t.Errorf("stripSQL = %q, want %q", got, want)
	}
}
