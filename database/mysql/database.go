// Package mysql constructs a lazy MySQL pool using go-sql-driver/mysql.
package mysql

import (
	"errors"
	"strings"

	driver "github.com/go-sql-driver/mysql"
	"github.com/maxtanforge/go-frame/database"
)

// Options configures the driver connection string and shared SQL pool.
type Options struct {
	DSN  string
	Pool database.Options
}

// Open validates connection configuration without establishing a connection.
// Construction errors omit driver details because they may contain credentials.
func Open(o Options) (*database.DB, error) {
	if strings.TrimSpace(o.DSN) == "" {
		return nil, errors.New("mysql: DSN is required")
	}
	config, err := driver.ParseDSN(o.DSN)
	if err != nil {
		return nil, errors.New("mysql: invalid DSN")
	}
	connector, err := driver.NewConnector(config)
	if err != nil {
		return nil, errors.New("mysql: invalid connection configuration")
	}
	return database.Open(connector, o.Pool)
}
