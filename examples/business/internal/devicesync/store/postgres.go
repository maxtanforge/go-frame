// Package store implements device event state using an application-owned
// PostgreSQL schema. The caller owns the pool and applies schema.sql explicitly.
package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/examples/business/internal/devicesync"
)

// Store borrows a pool. It does not own migrations, credentials or retries.
type Store struct{ db *database.DB }

// New checks the dependency without connecting to PostgreSQL.
func New(db *database.DB) (*Store, error) {
	if db.SQLDB() == nil {
		return nil, errors.New("device store requires a database")
	}
	return &Store{db: db}, nil
}

const selectEvent = "SELECT id, user_id, steps, delivered FROM device_events WHERE id = $1"

// Put atomically inserts or reads the original immutable event. A retry after
// an ambiguous commit uses the same ID, never a new generated key.
func (s *Store) Put(ctx context.Context, event devicesync.Event) (devicesync.Record, error) {
	if err := event.Validate(); err != nil {
		return devicesync.Record{}, err
	}
	var result devicesync.Record
	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO device_events (id, user_id, steps) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING", event.ID, event.UserID, event.Steps); err != nil {
			return err
		}
		var err error
		result, err = scan(tx.QueryRowContext(ctx, selectEvent, event.ID))
		if err != nil {
			return err
		}
		if result.Event != event {
			return devicesync.ErrConflict
		}
		return nil
	})
	if err != nil {
		return devicesync.Record{}, err
	}
	return result, nil
}

// Find returns a value snapshot and distinguishes absence from storage failure.
func (s *Store) Find(ctx context.Context, id string) (devicesync.Record, error) {
	return scan(s.db.SQLDB().QueryRowContext(ctx, selectEvent, id))
}
func scan(row *sql.Row) (devicesync.Record, error) {
	var r devicesync.Record
	err := row.Scan(&r.Event.ID, &r.Event.UserID, &r.Event.Steps, &r.Delivered)
	if errors.Is(err, sql.ErrNoRows) {
		return devicesync.Record{}, devicesync.ErrNotFound
	}
	return r, err
}

// Complete acknowledges remote delivery. It runs after the remote call, never
// in a transaction held across the network. Concurrent acknowledgments are safe.
func (s *Store) Complete(ctx context.Context, id string) error {
	result, err := s.db.SQLDB().ExecContext(ctx, "UPDATE device_events SET delivered = TRUE WHERE id = $1", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return devicesync.ErrNotFound
	}
	return nil
}

// Pending selects a bounded replay batch. Business scheduling must provide
// backoff/fairness for persistently failing events; this query is not a scheduler.
func (s *Store) Pending(ctx context.Context, limit int) (ids []string, err error) {
	if limit < 1 || limit > 100 {
		return nil, devicesync.ErrInvalid
	}
	rows, err := s.db.SQLDB().QueryContext(ctx, "SELECT id, user_id, steps, delivered FROM device_events WHERE delivered = FALSE ORDER BY id LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	ids = make([]string, 0, limit)
	for rows.Next() {
		var r devicesync.Record
		if err := rows.Scan(&r.Event.ID, &r.Event.UserID, &r.Event.Steps, &r.Delivered); err != nil {
			return nil, err
		}
		ids = append(ids, r.Event.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

var _ devicesync.Store = (*Store)(nil)
