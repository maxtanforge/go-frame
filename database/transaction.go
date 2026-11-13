package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/maxtanforge/go-frame/database/internal/txcleanup"
)

// WithTx commits a successful callback, or rolls back on error, panic, or goroutine exit.
// The callback must not commit or roll back the transaction itself.
func (d *DB) WithTx(ctx context.Context, opts *sql.TxOptions, fn func(*sql.Tx) error) error {
	if ctx == nil {
		return errors.New("database: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.SQLDB() == nil {
		return errors.New("database: not configured")
	}
	if fn == nil {
		return errors.New("database: transaction callback is required")
	}
	tx, err := d.sqlDB.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("database: begin transaction: %w", err)
	}
	// Rollback is a no-op after Commit or the explicit rollback below.
	defer func() { _ = tx.Rollback() }()
	if err := ctx.Err(); err != nil {
		return err
	}
	err = fn(tx)
	if canceled := ctx.Err(); canceled != nil {
		if err == nil {
			err = canceled
		} else if !errors.Is(err, canceled) {
			err = errors.Join(err, canceled)
		}
	}
	if err != nil {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !txcleanup.OnlyCompleted(rollbackErr) {
			return errors.Join(err, fmt.Errorf("database: rollback: %w", rollbackErr))
		}
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("database: commit: %w", err)
	}
	return nil
}
