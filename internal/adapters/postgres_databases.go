package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Snarap1/hqdb/internal/config"
)

// discoverDatabasesQuery lists the Databases a login can open (ADR-0004).
const discoverDatabasesQuery = `SELECT datname FROM pg_database
WHERE datallowconn AND NOT datistemplate
  AND has_database_privilege(datname, 'CONNECT')
ORDER BY datname`

// BindsDatabase reports whether an adapter's connection is bound to one
// Database, so reaching another Database of the same Profile takes another
// connection: postgres today (ADR-0004). It is the one place that decides
// this; mssql has the same shape and would be added here.
func BindsDatabase(adapter string) bool { return adapter == "postgres" }

// PathDatabase is the Database a catalog path names: its first element, or
// "" for the root. Callers apply it only where BindsDatabase holds.
func PathDatabase(path []string) string {
	if len(path) > 0 {
		return path[0]
	}
	return ""
}

func openPostgres(p map[string]any) (DB, error) {
	c, err := connectPostgres(p, "")
	if err != nil {
		return nil, err
	}
	return c, nil
}

// connectPostgres opens a connection bound to database ("" for the
// Profile's own dbname, or the login's default).
func connectPostgres(p map[string]any, database string) (*SQL, error) {
	dsn, err := postgresDSN(p, database)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return &SQL{kind: "postgres", db: db, profile: p, database: database}, nil
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

// errNoDiscovery is the refusal for an adapter without Database discovery.
func errNoDiscovery(adapter string) error {
	return fmt.Errorf("database discovery is not available for %s", adapter)
}

// Databases returns the Databases a postgres Profile exposes, sorted. With
// dbname it is only that one. Without, it lists what the login can open
// (see SQL.Databases). Other adapters are an error.
func Databases(ctx context.Context, p map[string]any) ([]string, error) {
	adapter, err := config.Validate(p)
	if err != nil {
		return nil, err
	}
	if !BindsDatabase(adapter) {
		return nil, errNoDiscovery(adapter)
	}
	conn, err := connectPostgres(p, "")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.Databases(ctx)
}

// Databases is the package-level Databases on a connection that is already
// open: the discovery query runs on it first, and only if the login's
// default database is missing, closed to it, or rejected by pg_hba.conf does
// discovery retry against `postgres`. Any other failure (credentials,
// network, cancellation) is returned as is.
func (c *SQL) Databases(ctx context.Context) ([]string, error) {
	if c.profile == nil {
		return nil, errNoDiscovery(c.kind)
	}
	configured, err := configuredDatabase(c.profile)
	if err != nil {
		return nil, err
	}
	if configured != "" {
		return []string{configured}, nil
	}
	return discoverWithRetry(ctx, func(ctx context.Context, database string) ([]string, error) {
		if database == "" {
			return queryDatabases(ctx, c.db)
		}
		other, err := connectPostgres(c.profile, database)
		if err != nil {
			return nil, err
		}
		defer other.Close()
		return queryDatabases(ctx, other.db)
	})
}

// discoverWithRetry runs discover against the login's default database
// ("") and, when that database is unusable (see DefaultDatabaseUnusable),
// against `postgres`.
func discoverWithRetry(ctx context.Context, discover func(ctx context.Context, database string) ([]string, error)) ([]string, error) {
	names, err := discover(ctx, "")
	if err == nil || !DefaultDatabaseUnusable(err) {
		return names, err
	}
	names, err2 := discover(ctx, "postgres")
	if err2 == nil {
		return names, nil
	}
	// The default-database failure is the one a user can act on.
	return nil, fmt.Errorf("%w (retry against database postgres also failed: %v)", err, err2)
}

// DefaultDatabaseUnusable reports whether err says the login's default
// database does not exist (SQLSTATE 3D000), the login lacks CONNECT on it
// (42501), or pg_hba.conf has no entry for it (28000), the cases where
// `postgres` is worth trying. pg_hba.conf matches on the database, and the
// default one is named after the login, which a server often does not list.
func DefaultDatabaseUnusable(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (pe.Code == "3D000" || pe.Code == "42501" || pe.Code == "28000")
}

func queryDatabases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, discoverDatabasesQuery)
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
// An empty database, or an adapter whose connection is not bound to a
// Database (anything but postgres), behaves exactly like Open: the argument
// is ignored there. For postgres the requested Database must be the
// configured dbname or one that discovery lists, otherwise the error names
// the available ones.
func OpenDatabase(ctx context.Context, p map[string]any, database string) (DB, error) {
	adapter, err := config.Validate(p)
	if err != nil {
		return nil, err
	}
	if database == "" || !BindsDatabase(adapter) {
		return Open(p)
	}
	c, err := openPostgresIn(ctx, p, database)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// openPostgresIn opens a connection to a non-empty database of a postgres
// Profile, after checking the Profile exposes it.
func openPostgresIn(ctx context.Context, p map[string]any, database string) (*SQL, error) {
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
	return connectPostgres(p, database)
}

// ForDatabase returns a connection bound to database: c itself when it
// already is, or when the adapter's connections are not bound to a Database
// (database is ignored there); otherwise a fresh sibling connection from
// the same Profile, refused if the Profile does not expose that Database.
// The returned func releases whatever was opened and is always safe to call.
func (c *SQL) ForDatabase(ctx context.Context, database string) (*SQL, func(), error) {
	noop := func() {}
	if database == "" || c.profile == nil || c.database == database {
		return c, noop, nil
	}
	other, err := openPostgresIn(ctx, c.profile, database)
	if err != nil {
		return nil, noop, err
	}
	return other, other.Close, nil
}
