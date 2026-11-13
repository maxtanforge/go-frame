// Package database provides a driver-independent SQL connection pool.
package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/maxtanforge/go-frame/internal/isnil"
	"sync"
	"time"
)

// Options configures a pool. Zero pool limits and durations use bounded defaults.
type Options struct {
	MaxOpenConns    int           // default 25
	MaxIdleConns    int           // zero uses min(5, MaxOpenConns); an explicit "no idle connections" is not expressible (mirrors stdlib nonzero-idle defaults)
	ConnMaxIdleTime time.Duration // default 5 minutes
	ConnMaxLifetime time.Duration // default 30 minutes
}

// DB owns a SQL connection pool. Do not copy after first use.
type DB struct {
	sqlDB     *sql.DB
	closeOnce sync.Once
	closeErr  error
}

// Open validates options and creates a lazy pool. Call Ping to verify connectivity.
// On success DB owns the connector's pool; on validation failure ownership stays
// with the caller. The connector must not be nil (including a typed nil).
func Open(connector driver.Connector, o Options) (*DB, error) {
	if isnil.Check(connector) {
		return nil, errors.New("database: connector is required")
	}
	if o.MaxOpenConns < 0 || o.MaxIdleConns < 0 || o.ConnMaxIdleTime < 0 || o.ConnMaxLifetime < 0 {
		return nil, errors.New("database: pool limits and durations must be nonnegative")
	}
	if o.MaxOpenConns == 0 {
		o.MaxOpenConns = 25
	}
	if o.MaxIdleConns == 0 {
		o.MaxIdleConns = min(5, o.MaxOpenConns)
	}
	if o.MaxIdleConns > o.MaxOpenConns {
		return nil, errors.New("database: MaxIdleConns must not exceed MaxOpenConns")
	}
	if o.ConnMaxIdleTime == 0 {
		o.ConnMaxIdleTime = 5 * time.Minute
	}
	if o.ConnMaxLifetime == 0 {
		o.ConnMaxLifetime = 30 * time.Minute
	}
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(o.MaxOpenConns)
	pool.SetMaxIdleConns(o.MaxIdleConns)
	pool.SetConnMaxIdleTime(o.ConnMaxIdleTime)
	pool.SetConnMaxLifetime(o.ConnMaxLifetime)
	return &DB{sqlDB: pool}, nil
}

// SQLDB lends the underlying pool for queries or an ORM chosen by the caller.
// The caller must not close it; DB retains ownership.
func (d *DB) SQLDB() *sql.DB {
	if d == nil {
		return nil
	}
	return d.sqlDB
}

// Ping verifies a live connection within ctx and may establish a connection
// through the lazy pool. It does not replace the pool or transfer ownership.
func (d *DB) Ping(ctx context.Context) error {
	if d.SQLDB() == nil {
		return errors.New("database: not configured")
	}
	return d.sqlDB.PingContext(ctx)
}

// Close is nil-safe and returns the same result on subsequent calls.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	d.closeOnce.Do(func() {
		if d.sqlDB != nil {
			d.closeErr = d.sqlDB.Close()
		}
	})
	return d.closeErr
}
