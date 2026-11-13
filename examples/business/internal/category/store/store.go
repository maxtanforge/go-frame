// Package store persists business category snapshots with native Ent queries.
package store

import (
	"context"
	"database/sql"
	"errors"

	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	generated "github.com/maxtanforge/go-frame/examples/business/internal/ent"
	"github.com/maxtanforge/go-frame/examples/business/internal/ent/book"
	entity "github.com/maxtanforge/go-frame/examples/business/internal/ent/category"
)

// Store uses the caller's configured Ent driver and transaction boundaries.
type Store struct{ client *generated.Client }

var _ category.CatalogRepository = (*Store)(nil)

// New requires a generated client configured with a SQL driver.
func New(client *generated.Client) (*Store, error) {
	if client == nil {
		return nil, errors.New("category store: Ent client required")
	}
	return &Store{client: client}, nil
}
func snapshot(v *generated.Category) category.Model {
	id, _ := category.NewID(v.ID)
	parent, _ := category.NewID(v.ParentID)
	m := category.Model{ID: id, ParentID: parent, Owner: v.Owner, Name: v.Name, System: v.System}
	if v.Note != nil {
		m.Note = *v.Note
		m.HasNote = true
	}
	return m
}
func translated(err error) error {
	if generated.IsNotFound(err) {
		return errors.Join(category.ErrNotFound, err)
	}
	return err
}
func validModel(m category.Model) error {
	if !m.ID.Valid() || !m.ParentID.Valid() || m.Owner == "" || len(m.Owner) > 128 {
		return category.ErrInvalid
	}
	if err := category.ValidateName(m.Name); err != nil {
		return err
	}
	if m.HasNote {
		return category.ValidateNote(m.Note)
	}
	return nil
}

// Find returns a value snapshot without sharing a mutable entity pointer.
func (s *Store) Find(ctx context.Context, id category.ID) (category.Model, error) {
	if !id.Valid() {
		return category.Model{}, category.ErrInvalid
	}
	v, err := s.client.Category.Get(ctx, id.Int64())
	if err != nil {
		return category.Model{}, translated(err)
	}
	return snapshot(v), nil
}

// Create locks the parent and authorizes before inserting a user category.
func (s *Store) Create(ctx context.Context, m category.Model, check func(string) error) (category.Model, error) {
	if check == nil || m.System {
		return category.Model{}, category.ErrInvalid
	}
	if err := validModel(m); err != nil {
		return category.Model{}, err
	}
	var result category.Model
	err := entdb.WithTx(ctx, s.client.Tx, func(tx *generated.Tx) error {
		client := tx.Client()
		parent, err := client.Book.Query().Where(book.IDEQ(m.ParentID.Int64())).ForUpdate().Only(ctx)
		if err != nil {
			return translated(err)
		}
		if err := check(parent.Owner); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.Owner != parent.Owner {
			return category.ErrForbidden
		}
		create := client.Category.Create().SetID(m.ID.Int64()).SetParentID(m.ParentID.Int64()).SetOwner(m.Owner).SetName(m.Name).SetSystem(false)
		if m.HasNote {
			create.SetNote(m.Note)
		}
		v, err := create.Save(ctx)
		if err != nil {
			return translated(err)
		}
		result = snapshot(v)
		return nil
	})
	if err != nil {
		return category.Model{}, err
	}
	return result, nil
}

// Update locks, runs the business callback, and persists only name and note.
func (s *Store) Update(ctx context.Context, id category.ID, change func(*category.Model) error) (category.Model, error) {
	if !id.Valid() || change == nil {
		return category.Model{}, category.ErrInvalid
	}
	var result category.Model
	err := entdb.WithTx(ctx, s.client.Tx, func(tx *generated.Tx) error {
		client := tx.Client()
		v, err := client.Category.Query().Where(entity.IDEQ(id.Int64())).ForUpdate().Only(ctx)
		if err != nil {
			return translated(err)
		}
		before := snapshot(v)
		after := before
		if err := change(&after); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if before.ID != after.ID || before.ParentID != after.ParentID || before.Owner != after.Owner || before.System != after.System {
			return category.ErrImmutable
		}
		if err := validModel(after); err != nil {
			return err
		}
		update := client.Category.UpdateOneID(id.Int64()).SetName(after.Name)
		if after.HasNote {
			update.SetNote(after.Note)
		} else {
			update.ClearNote()
		}
		saved, err := update.Save(ctx)
		if err != nil {
			return translated(err)
		}
		result = snapshot(saved)
		return nil
	})
	if err != nil {
		return category.Model{}, err
	}
	return result, nil
}

// Delete checks a locked snapshot before removing it.
func (s *Store) Delete(ctx context.Context, id category.ID, check func(category.Model) error) error {
	if !id.Valid() || check == nil {
		return category.ErrInvalid
	}
	return entdb.WithTx(ctx, s.client.Tx, func(tx *generated.Tx) error {
		client := tx.Client()
		v, err := client.Category.Query().Where(entity.IDEQ(id.Int64())).ForUpdate().Only(ctx)
		if err != nil {
			return translated(err)
		}
		if err := check(snapshot(v)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return translated(client.Category.DeleteOneID(id.Int64()).Exec(ctx))
	})
}

// List authorizes the parent in a ReadCommitted transaction, then counts and
// reads in ascending ID order. Count and records can see different commits.
func (s *Store) List(ctx context.Context, parentID category.ID, offset, size int64, check func(string) error) ([]category.Model, int64, error) {
	maxInt := int64(int(^uint(0) >> 1))
	if !parentID.Valid() || check == nil || offset < 0 || offset > 1_000_000 || offset > maxInt || size < 1 || size > 100 || size > maxInt {
		return nil, 0, category.ErrInvalid
	}
	result := make([]category.Model, 0)
	var total int
	begin := func(ctx context.Context) (*generated.Tx, error) {
		return s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	}
	err := entdb.WithTx(ctx, begin, func(tx *generated.Tx) error {
		client := tx.Client()
		parent, err := client.Book.Get(ctx, parentID.Int64())
		if err != nil {
			return translated(err)
		}
		if err := check(parent.Owner); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		q := client.Category.Query().Where(entity.ParentIDEQ(parentID.Int64()), entity.OwnerEQ(parent.Owner))
		total, err = q.Clone().Count(ctx)
		if err != nil {
			return err
		}
		rows, err := q.Order(generated.Asc(entity.FieldID)).Offset(int(offset)).Limit(int(size)).All(ctx)
		if err != nil {
			return err
		}
		for _, v := range rows {
			result = append(result, snapshot(v))
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return result, int64(total), nil
}
