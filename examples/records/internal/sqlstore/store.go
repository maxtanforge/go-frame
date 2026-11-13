// Package sqlstore implements the record service's persistence contracts.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/database/query"
	"github.com/maxtanforge/go-frame/examples/records/internal/record"
	"github.com/maxtanforge/go-frame/pagination"
)

type Store struct {
	db                  *database.DB
	dialect             query.Dialect
	insert, audit, find string
}

func New(db *database.DB, dialect string) (*Store, error) {
	if db == nil || db.SQLDB() == nil {
		return nil, errors.New("records: database is required")
	}
	s := &Store{db: db}
	switch dialect {
	case "mysql":
		s.dialect = query.MySQL
		s.insert = "INSERT INTO frame_records (id, title, created_at, updated_at, created_by_kind, created_by_id, updated_by_kind, updated_by_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"
		s.audit = "INSERT INTO frame_record_audit (record_id, action, actor_kind, actor_id, occurred_at) VALUES (?, ?, ?, ?, ?)"
		s.find = "SELECT id, title, created_at, updated_at, created_by_kind, created_by_id, updated_by_kind, updated_by_id FROM frame_records WHERE id = ?"
	case "postgres":
		s.dialect = query.Postgres
		s.insert = "INSERT INTO frame_records (id, title, created_at, updated_at, created_by_kind, created_by_id, updated_by_kind, updated_by_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)"
		s.audit = "INSERT INTO frame_record_audit (record_id, action, actor_kind, actor_id, occurred_at) VALUES ($1, $2, $3, $4, $5)"
		s.find = "SELECT id, title, created_at, updated_at, created_by_kind, created_by_id, updated_by_kind, updated_by_id FROM frame_records WHERE id = $1"
	default:
		return nil, errors.New("records: unsupported SQL dialect")
	}
	return s, nil
}

// List explicitly fetches one probe row; no count query or SQL rewriting occurs.
func (s *Store) List(ctx context.Context, w pagination.Window, sorts []pagination.Sort) (result []record.Record, runErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	order, err := query.OrderBy(s.dialect, sorts, map[string]string{"id": "id", "title": "title"}, []pagination.Sort{{Field: "id"}})
	if err != nil {
		return nil, err
	}
	limit, args, err := query.LimitOffset(s.dialect, w, 1, true)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.SQLDB().QueryContext(ctx, "SELECT id, title, created_at, updated_at, created_by_kind, created_by_id, updated_by_kind, updated_by_id FROM frame_records"+order+limit, args...)
	if err != nil {
		return nil, err
	}
	defer func() { runErr = errors.Join(runErr, rows.Close()) }()
	result = make([]record.Record, 0)
	// Bound memory and reads even if an unexpected driver ignores LIMIT.
	for rows.Next() {
		if int64(len(result)) == w.FetchSize() {
			return nil, errors.New("records: unexpected row count")
		}
		var row record.Record
		if err := scanRecord(rows, &row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) Find(ctx context.Context, id int64) (record.Record, error) {
	var result record.Record
	err := scanRecord(s.db.SQLDB().QueryRowContext(ctx, s.find, id), &result)
	if errors.Is(err, sql.ErrNoRows) {
		return record.Record{}, record.ErrNotFound
	}
	if err != nil {
		return record.Record{}, err
	}
	return result, nil
}

func (s *Store) WithinTransaction(ctx context.Context, fn func(record.Writer) error) error {
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error { return fn(writer{tx: tx, store: s}) })
}

type writer struct {
	tx    *sql.Tx
	store *Store
}

func (w writer) Insert(ctx context.Context, r record.Record) error {
	_, err := w.tx.ExecContext(ctx, w.store.insert, r.ID, r.Title, r.CreatedAt, r.UpdatedAt, r.CreatedBy.Kind, r.CreatedBy.ID, r.UpdatedBy.Kind, r.UpdatedBy.ID)
	return err
}
func (w writer) AuditCreated(ctx context.Context, r record.Record) error {
	_, err := w.tx.ExecContext(ctx, w.store.audit, r.ID, "created", r.CreatedBy.Kind, r.CreatedBy.ID, r.CreatedAt)
	return err
}

func scanRecord(scanner interface{ Scan(...any) error }, r *record.Record) error {
	if err := scanner.Scan(&r.ID, &r.Title, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy.Kind, &r.CreatedBy.ID, &r.UpdatedBy.Kind, &r.UpdatedBy.ID); err != nil {
		return err
	}
	if r.ID <= 0 {
		return errors.New("records: invalid stored ID")
	}
	r.CreatedAt = r.CreatedAt.UTC()
	r.UpdatedAt = r.UpdatedAt.UTC()
	return r.Validate()
}
