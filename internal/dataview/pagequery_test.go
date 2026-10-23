package dataview

import (
	"strings"
	"testing"
)

func TestBuildPageQueryPerAdapter(t *testing.T) {
	cases := []struct {
		name    string
		adapter string
		q       PageQuery
		want    string
	}{
		{
			name:    "postgres",
			adapter: "postgres",
			q:       PageQuery{Path: []string{"hqdb", "public", "widgets"}, PageSize: 30, Offset: 0},
			want:    `SELECT * FROM "public"."widgets" LIMIT 30 OFFSET 0`,
		},
		{
			name:    "mysql",
			adapter: "mysql",
			q:       PageQuery{Path: []string{"hqdb", "widgets"}, PageSize: 30, Offset: 30},
			want:    "SELECT * FROM `hqdb`.`widgets` LIMIT 30 OFFSET 30",
		},
		{
			name:    "clickhouse",
			adapter: "clickhouse",
			q:       PageQuery{Path: []string{"hqdb", "widgets"}, PageSize: 10, Offset: 20},
			want:    "SELECT * FROM `hqdb`.`widgets` LIMIT 10 OFFSET 20",
		},
		{
			name:    "mssql default ordering",
			adapter: "mssql",
			q:       PageQuery{Path: []string{"hqdb", "dbo", "widgets"}, PageSize: 30, Offset: 0},
			want:    `SELECT * FROM [dbo].[widgets] ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 30 ROWS ONLY`,
		},
		{
			name:    "mssql explicit order by replaces the default",
			adapter: "mssql",
			q:       PageQuery{Path: []string{"hqdb", "dbo", "widgets"}, PageSize: 30, Offset: 0, OrderBy: "id DESC"},
			want:    `SELECT * FROM [dbo].[widgets] ORDER BY id DESC OFFSET 0 ROWS FETCH NEXT 30 ROWS ONLY`,
		},
		{
			name:    "odbc behaves like mssql",
			adapter: "odbc",
			q:       PageQuery{Path: []string{"hqdb", "dbo", "widgets"}, PageSize: 5, Offset: 10},
			want:    `SELECT * FROM [dbo].[widgets] ORDER BY (SELECT NULL) OFFSET 10 ROWS FETCH NEXT 5 ROWS ONLY`,
		},
		{
			name:    "where clause inserted verbatim",
			adapter: "postgres",
			q:       PageQuery{Path: []string{"hqdb", "public", "widgets"}, PageSize: 30, Where: "price > 1"},
			want:    `SELECT * FROM "public"."widgets" WHERE price > 1 LIMIT 30 OFFSET 0`,
		},
		{
			name:    "negative offset clamps to zero",
			adapter: "postgres",
			q:       PageQuery{Path: []string{"hqdb", "public", "widgets"}, PageSize: 30, Offset: -5},
			want:    `SELECT * FROM "public"."widgets" LIMIT 30 OFFSET 0`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildPageQuery(tc.adapter, tc.q)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildPageQueryQuotesIdentifiersThatNeedIt(t *testing.T) {
	cases := []struct {
		name    string
		adapter string
		path    []string
		want    string
	}{
		{"postgres embedded quote", "postgres", []string{"hqdb", "my\"schema", "my\"table"}, `"my""schema"."my""table"`},
		{"mysql embedded backtick", "mysql", []string{"my`db", "my`table"}, "`my``db`.`my``table`"},
		{"mssql embedded bracket", "mssql", []string{"hqdb", "my]schema", "my]table"}, `[my]]schema].[my]]table]`},
		{"clickhouse reserved word", "clickhouse", []string{"order", "select"}, "`order`.`select`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildPageQuery(tc.adapter, PageQuery{Path: tc.path, PageSize: 10})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("got %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestBuildPageQueryRejectsNonPositivePageSize(t *testing.T) {
	for _, size := range []int{0, -1} {
		if _, err := BuildPageQuery("postgres", PageQuery{Path: []string{"a", "b"}, PageSize: size}); err == nil {
			t.Errorf("page size %d: want an error", size)
		}
	}
}

func TestBuildPageQueryRejectsShortPath(t *testing.T) {
	if _, err := BuildPageQuery("postgres", PageQuery{Path: []string{"onlyone"}, PageSize: 30}); err == nil {
		t.Error("want an error for a path with fewer than 2 segments")
	}
}

func TestBuildPageQueryRejectsUnsupportedAdapter(t *testing.T) {
	for _, adapter := range []string{"redis", "mongodb", "sqlite"} {
		if _, err := BuildPageQuery(adapter, PageQuery{Path: []string{"a", "b"}, PageSize: 30}); err == nil {
			t.Errorf("adapter %q: want an error", adapter)
		}
	}
}

func TestBuildPageQueryWhereAndOrderByPerAdapter(t *testing.T) {
	cases := []struct {
		adapter string
		path    []string
		want    string
	}{
		{"postgres", []string{"hqdb", "public", "widgets"}, `SELECT * FROM "public"."widgets" WHERE price > 1 ORDER BY name DESC LIMIT 30 OFFSET 0`},
		{"mysql", []string{"hqdb", "widgets"}, "SELECT * FROM `hqdb`.`widgets` WHERE price > 1 ORDER BY name DESC LIMIT 30 OFFSET 0"},
		{"clickhouse", []string{"hqdb", "widgets"}, "SELECT * FROM `hqdb`.`widgets` WHERE price > 1 ORDER BY name DESC LIMIT 30 OFFSET 0"},
		{"mssql", []string{"hqdb", "dbo", "widgets"}, `SELECT * FROM [dbo].[widgets] WHERE price > 1 ORDER BY name DESC OFFSET 0 ROWS FETCH NEXT 30 ROWS ONLY`},
		{"odbc", []string{"hqdb", "dbo", "widgets"}, `SELECT * FROM [dbo].[widgets] WHERE price > 1 ORDER BY name DESC OFFSET 0 ROWS FETCH NEXT 30 ROWS ONLY`},
	}
	for _, tc := range cases {
		t.Run(tc.adapter, func(t *testing.T) {
			got, err := BuildPageQuery(tc.adapter, PageQuery{Path: tc.path, PageSize: 30, Where: "price > 1", OrderBy: "name DESC"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildCountQueryPerAdapter(t *testing.T) {
	cases := []struct {
		name    string
		adapter string
		path    []string
		where   string
		want    string
	}{
		{"postgres no where", "postgres", []string{"hqdb", "public", "widgets"}, "", `SELECT COUNT(*) FROM "public"."widgets"`},
		{"postgres where", "postgres", []string{"hqdb", "public", "widgets"}, "price > 1", `SELECT COUNT(*) FROM "public"."widgets" WHERE price > 1`},
		{"mysql where", "mysql", []string{"hqdb", "widgets"}, "id = 2", "SELECT COUNT(*) FROM `hqdb`.`widgets` WHERE id = 2"},
		{"clickhouse where", "clickhouse", []string{"hqdb", "widgets"}, "id = 2", "SELECT COUNT(*) FROM `hqdb`.`widgets` WHERE id = 2"},
		{"mssql where", "mssql", []string{"hqdb", "dbo", "widgets"}, "id = 2", `SELECT COUNT(*) FROM [dbo].[widgets] WHERE id = 2`},
		{"odbc no where", "odbc", []string{"hqdb", "dbo", "widgets"}, "", `SELECT COUNT(*) FROM [dbo].[widgets]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildCountQuery(tc.adapter, tc.path, tc.where)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildCountQueryRejectsUnsupportedAdapterAndShortPath(t *testing.T) {
	if _, err := BuildCountQuery("redis", []string{"a", "b"}, ""); err == nil {
		t.Error("redis: want an error")
	}
	if _, err := BuildCountQuery("postgres", []string{"a"}, ""); err == nil {
		t.Error("short path: want an error")
	}
}

func TestBuildExportQueryHasNoPagingClause(t *testing.T) {
	cases := []struct {
		name, adapter, where, orderBy string
		path                          []string
		want                          string
	}{
		{"postgres filtered and sorted", "postgres", "id < 3", "id DESC", []string{"hqdb", "public", "widgets"},
			`SELECT * FROM "public"."widgets" WHERE id < 3 ORDER BY id DESC`},
		{"mssql needs no default order without paging", "mssql", "", "", []string{"hqdb", "dbo", "widgets"},
			`SELECT * FROM [dbo].[widgets]`},
		{"mysql sorted only", "mysql", "", "id", []string{"hqdb", "widgets"},
			"SELECT * FROM `hqdb`.`widgets` ORDER BY id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := BuildExportQuery(c.adapter, c.path, c.where, c.orderBy)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	if _, err := BuildExportQuery("redis", []string{"a", "b"}, "", ""); err == nil {
		t.Error("redis: want an error")
	}
}
