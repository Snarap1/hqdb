package console

import "testing"

func TestReturnsRows(t *testing.T) {
	cases := []struct {
		stmt string
		want bool
	}{
		{"SELECT 1", true},
		{"  select * from t", true},
		{"-- note\nSELECT 1", true},
		{"/* x */ (SELECT 1)", true},
		{"WITH a AS (SELECT 1) SELECT * FROM a", true},
		{"SHOW search_path", true},
		{"EXPLAIN SELECT 1", true},
		{"DESCRIBE t", true},
		{"VALUES (1)", true},
		{"INSERT INTO t VALUES (1)", false},
		{"UPDATE t SET a = 1", false},
		{"DELETE FROM t", false},
		{"SET search_path TO x", false},
		{"CREATE TEMP TABLE t (a int)", false},
		{"INSERT INTO t VALUES (1) RETURNING id", true},
		{"INSERT INTO t OUTPUT inserted.id VALUES (1)", true},
		{"UPDATE t SET a = 'returning'", false},
		{"UPDATE t SET a = 1 -- returning", false},
		{"", false},
	}
	for _, c := range cases {
		if got := returnsRows(c.stmt); got != c.want {
			t.Errorf("returnsRows(%q) = %v, want %v", c.stmt, got, c.want)
		}
	}
}
