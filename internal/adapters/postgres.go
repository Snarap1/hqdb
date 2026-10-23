package adapters

import (
	"strconv"

	"github.com/Snarap1/hqdb/internal/config"
)

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
