// Package entdb integrates business-owned Ent clients with Frame infrastructure.
package entdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	framelog "github.com/maxtanforge/go-frame/telemetry/logger"
)

// Options selects a dialect without importing a concrete database driver.
type Options struct {
	Dialect       string
	Logger        *slog.Logger
	SlowThreshold time.Duration
}

// Driver borrows a pool. Only the pool owner closes it; Close is a no-op.
// Do not enable Ent Debug on a client using this driver: Debug independently
// prints SQL and arguments before this driver's safe logging runs.
type Driver struct {
	driver *entsql.Driver
	log    queryLog
}

var _ dialect.Driver = (*Driver)(nil)

// New performs no I/O, pool configuration, schema migration or registration.
func New(pool *sql.DB, opts Options) (*Driver, error) {
	if pool == nil {
		return nil, errors.New("ent: nil pool")
	}
	if opts.Dialect != dialect.MySQL && opts.Dialect != dialect.Postgres {
		return nil, errors.New("ent: unsupported dialect")
	}
	if opts.SlowThreshold < 0 {
		return nil, errors.New("ent: negative slow threshold")
	}
	if opts.SlowThreshold == 0 {
		opts.SlowThreshold = 200 * time.Millisecond
	}
	return &Driver{driver: entsql.OpenDB(opts.Dialect, pool), log: queryLog{base: opts.Logger, slow: opts.SlowThreshold}}, nil
}

// Close does not close the borrowed pool; the pool owner keeps that duty.
func (d *Driver) Close() error { return nil }

// Dialect forwards the wrapped Ent driver's dialect.
func (d *Driver) Dialect() string { return d.driver.Dialect() }

// Exec runs one statement through the logging wrapper.
func (d *Driver) Exec(ctx context.Context, q string, args, v any) error {
	start := time.Now()
	err := d.driver.Exec(ctx, q, args, v)
	d.log.record(ctx, "exec", start, err)
	return err
}

// Query runs one query through the logging wrapper; timing stops at Rows,
// later row iteration is not included.
func (d *Driver) Query(ctx context.Context, q string, args, v any) error {
	start := time.Now()
	err := d.driver.Query(ctx, q, args, v)
	d.log.record(ctx, "query", start, err)
	return err
}

// Tx starts a wrapped transaction with default options.
func (d *Driver) Tx(ctx context.Context) (dialect.Tx, error) { return d.BeginTx(ctx, nil) }

// BeginTx preserves native Ent isolation and read-only options.
func (d *Driver) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	start := time.Now()
	tx, err := d.driver.BeginTx(ctx, opts)
	d.log.record(ctx, "begin", start, err)
	if err != nil {
		return nil, err
	}
	return &loggedTx{tx: tx, ctx: ctx, log: d.log}, nil
}

type loggedTx struct {
	tx  dialect.Tx
	ctx context.Context
	log queryLog
}

func (t *loggedTx) Exec(ctx context.Context, q string, args, v any) error {
	start := time.Now()
	err := t.tx.Exec(ctx, q, args, v)
	t.log.record(ctx, "exec", start, err)
	return err
}
func (t *loggedTx) Query(ctx context.Context, q string, args, v any) error {
	start := time.Now()
	err := t.tx.Query(ctx, q, args, v)
	t.log.record(ctx, "query", start, err)
	return err
}
func (t *loggedTx) Commit() error {
	start := time.Now()
	err := t.tx.Commit()
	t.log.record(t.ctx, "commit", start, err)
	return err
}
func (t *loggedTx) Rollback() error {
	start := time.Now()
	err := t.tx.Rollback()
	t.log.record(t.ctx, "rollback", start, err)
	return err
}

type queryLog struct {
	base *slog.Logger
	slow time.Duration
}

func (l queryLog) record(ctx context.Context, operation string, start time.Time, err error) {
	elapsed := time.Since(start)
	level := slog.LevelWarn
	canceled := onlyCancellation(err)
	if canceled {
		level = slog.LevelDebug
	} else if err != nil {
		level = slog.LevelError
	} else if elapsed < l.slow {
		return
	}
	logger := framelog.FromContext(ctx)
	if logger == slog.Default() && l.base != nil {
		logger = l.base
	}
	// Query measures execution until rows are returned, not iteration or Scan.
	logger.Log(ctx, level, "ent database operation", "operation", operation, "duration_ms", float64(elapsed)/float64(time.Millisecond), "failed", err != nil && !canceled, "canceled", canceled)
}

// Joined errors can contain a cancellation and an independent database failure.
// Treat only a single wrapping chain ending at context.Canceled as cancellation.
func onlyCancellation(err error) bool {
	for err != nil {
		if err == context.Canceled { //nolint:errorlint // 手动逐层解包,逐层哨兵匹配即预期语义
			return true
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = wrapped.Unwrap()
	}
	return false
}
