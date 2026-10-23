package adapters

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/Snarap1/hqdb/internal/config"
)

// discoverDatabasesQuery lists the Databases a login can open (ADR-0004).
const discoverDatabasesQuery = `SELECT datname FROM pg_database
WHERE datallowconn AND NOT datistemplate
  AND has_database_privilege(datname, 'CONNECT')
ORDER BY datname`

// postgresDSN renders the libpq keyword/value connection string for a
// postgres Profile. A non-empty database overrides the Profile's dbname; an
// empty one leaves it as configured.
func postgresDSN(p map[string]any, database string) (string, error) {
	p, err := config.FillFromURL(p, "postgres")
	if err != nil {
		return "", err
	}
	host, port, err := hostPort(p, "localhost", 5432)
	if err != nil {
		return "", err
	}
	user, err := config.OptStr(p, "user")
	if err != nil {
		return "", err
	}
	pass, err := config.OptStr(p, "password")
	if err != nil {
		return "", err
	}
	dbname, err := config.DBOpt(p)
	if err != nil {
		return "", err
	}
	if database != "" {
		dbname = database
	}
	sslmode, err := config.OptStr(p, "sslmode")
	if err != nil {
		return "", err
	}
	if sslmode == "" {
		sslmode = "prefer"
	}
	// An empty dbname is omitted rather than written as `dbname=`: the pgx
	// keyword/value parser reads an empty value as "the next token", so a
	// bare `dbname=` swallows the following pair and the server then fails
	// with `database "sslmode=prefer" does not exist`. Omitting it leaves
	// Config.Database empty, so the startup message carries no database and
	// the server falls back to the login's default database.
	return pgDSN(
		pgKV("host", host),
		pgKV("port", strconv.Itoa(port)),
		pgKV("user", user),
		pgKV("password", pass),
		pgKV("dbname", dbname),
		pgKV("sslmode", sslmode),
	), nil
}

func openPostgres(p map[string]any) (DB, error) {
	c, err := openPostgresDatabase(p, "")
	if err != nil {
		return nil, err
	}
	return c, nil
}

func openPostgresDatabase(p map[string]any, database string) (*SQL, error) {
	dsn, err := postgresDSN(p, database)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return &SQL{kind: "postgres", db: db, profile: p}, nil
}

// configuredDatabase is the dbname a postgres Profile is narrowed to, or ""
// when it has none (including a URL conn_str without a database).
func configuredDatabase(p map[string]any) (string, error) {
	p, err := config.FillFromURL(p, "postgres")
	if err != nil {
		return "", err
	}
	return config.DBOpt(p)
}

// DiscoversDatabases reports whether the Profile exposes its Databases by
// discovery: a postgres Profile without dbname (ADR-0004). Other adapters,
// and a Profile that fails to parse, report false.
func DiscoversDatabases(p map[string]any) bool {
	if config.AdapterName(p) != "postgres" {
		return false
	}
	db, err := configuredDatabase(p)
	return err == nil && db == ""
}

// Databases returns the Databases a postgres Profile exposes, sorted. With
// dbname it is only that one. Without, it connects to the login's default
// database (then to `postgres` if that fails) and lists what the login can
// open. Other adapters are an error.
func Databases(ctx context.Context, p map[string]any) ([]string, error) {
	adapter, err := config.Validate(p)
	if err != nil {
		return nil, err
	}
	if adapter != "postgres" {
		return nil, fmt.Errorf("database discovery is not available for %s", adapter)
	}
	configured, err := configuredDatabase(p)
	if err != nil {
		return nil, err
	}
	if configured != "" {
		return []string{configured}, nil
	}
	names, err := discoverFrom(ctx, p, "")
	if err == nil {
		return names, nil
	}
	names, err2 := discoverFrom(ctx, p, "postgres")
	if err2 == nil {
		return names, nil
	}
	// The default-database failure is the one a user can act on.
	return nil, fmt.Errorf("%w (retry against database postgres also failed: %v)", err, err2)
}

func discoverFrom(ctx context.Context, p map[string]any, database string) ([]string, error) {
	conn, err := openPostgresDatabase(p, database)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.SQL().QueryContext(ctx, discoverDatabasesQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// CheckDatabase refuses a requested Database that is not in available,
// listing the ones that are.
func CheckDatabase(requested string, available []string) error {
	for _, a := range available {
		if a == requested {
			return nil
		}
	}
	return fmt.Errorf("database %q is not available for this profile; available: %s",
		requested, strings.Join(available, ", "))
}

// OpenDatabase opens a fresh connection to one Database of the Profile.
// An empty database, or an adapter that does not discover Databases
// (anything but postgres), behaves exactly like Open: the argument is
// ignored there. For postgres the requested Database must be the configured
// dbname or one that discovery lists, otherwise the error names the
// available ones.
func OpenDatabase(ctx context.Context, p map[string]any, database string) (DB, error) {
	adapter, err := config.Validate(p)
	if err != nil {
		return nil, err
	}
	if database == "" || adapter != "postgres" {
		return Open(p)
	}
	configured, err := configuredDatabase(p)
	if err != nil {
		return nil, err
	}
	if configured != database {
		avail, err := Databases(ctx, p)
		if err != nil {
			return nil, err
		}
		if err := CheckDatabase(database, avail); err != nil {
			return nil, err
		}
	}
	c, err := openPostgresDatabase(p, database)
	if err != nil {
		return nil, err
	}
	return c, nil
}
