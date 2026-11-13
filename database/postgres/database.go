// Package postgres constructs a lazy PostgreSQL pool using pgx's SQL adapter.
package postgres

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/maxtanforge/go-frame/database"
)

// Options configures the driver connection string and shared SQL pool.
type Options struct {
	DSN  string
	Pool database.Options
}

// Open accepts PostgreSQL URLs or keyword connection strings. Missing settings
// follow pgx defaults, including PG environment variables and service/pass files.
// It does not establish a connection. Construction errors omit credential-bearing
// driver details. Call Ping with a deadline to check connectivity.
func Open(o Options) (*database.DB, error) {
	if strings.TrimSpace(o.DSN) == "" {
		return nil, errors.New("postgres: DSN is required")
	}
	config, err := pgx.ParseConfig(o.DSN)
	if err != nil {
		return nil, errors.New("postgres: invalid connection configuration")
	}
	return database.Open(stdlib.GetConnector(*config), o.Pool)
}
