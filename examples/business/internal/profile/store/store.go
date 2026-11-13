// Package store persists profiles and change records in one PostgreSQL transaction.
package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/examples/business/internal/profile"
)

// Store uses a caller-owned database pool and never runs schema migrations.
type Store struct{ db *database.DB }

var _ profile.Repository = (*Store)(nil)

// New requires an initialized PostgreSQL database. See schema.sql for the tables.
func New(db *database.DB) (*Store, error) {
	if db == nil || db.SQLDB() == nil {
		return nil, errors.New("profile store: database required")
	}
	return &Store{db: db}, nil
}

// Update locks a snapshot and commits both the profile and its change record.
// Errors, including commit failures, return no success snapshot. The callback
// cannot change identity and must not retain its snapshot pointer.
func (s *Store) Update(ctx context.Context, id string, change func(*profile.Profile) error) (profile.Profile, error) {
	if !profile.ValidID(id) || change == nil {
		return profile.Profile{}, profile.ErrInvalid
	}
	var result profile.Profile
	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var current profile.Profile
		var note sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, display_name, age, note FROM profiles WHERE id = $1 FOR UPDATE`, id).Scan(&current.ID, &current.Basic.DisplayName, &current.Basic.Age, &note)
		if errors.Is(err, sql.ErrNoRows) {
			return profile.ErrNotFound
		}
		if err != nil {
			return err
		}
		current.Note, current.HasNote = note.String, note.Valid
		if err := change(&current); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if current.ID != id {
			return profile.ErrInvalid
		}
		var noteValue any
		if current.HasNote {
			noteValue = current.Note
		} else {
			current.Note = ""
		}
		updated, err := tx.ExecContext(ctx, `UPDATE profiles SET display_name = $1, age = $2, note = $3 WHERE id = $4`, current.Basic.DisplayName, current.Basic.Age, noteValue, id)
		if err != nil {
			return err
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return profile.ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO profile_changes (profile_id, display_name, age, note) VALUES ($1, $2, $3, $4)`, id, current.Basic.DisplayName, current.Basic.Age, noteValue)
		if err != nil {
			return err
		}
		result = current
		return nil
	})
	if err != nil {
		return profile.Profile{}, err
	}
	return result, nil
}
