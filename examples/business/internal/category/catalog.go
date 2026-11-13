package category

import (
	"context"
	"errors"
	"reflect"
)

// CatalogRepository owns atomic parent authorization and persistence. Create and
// List invoke check with the parent's owner within their transaction. List uses
// ascending category ID order; its numeric arguments are offset and size.
// Count and records may observe different committed snapshots at ReadCommitted.
// Delete invokes check with an independent snapshot before deleting. Callback
// errors or observed cancellation discard writes; callbacks never escape.
type CatalogRepository interface {
	Repository
	Create(context.Context, Model, func(parentOwner string) error) (Model, error)
	List(context.Context, ID, int64, int64, func(parentOwner string) error) ([]Model, int64, error)
	Delete(context.Context, ID, func(Model) error) error
}

// Catalog runs category creation, counted listing and deletion.
type Catalog struct {
	repository CatalogRepository
	nextID     func() (ID, error)
}

// ListQuery supplies explicit pagination values for non-HTTP callers.
type ListQuery struct {
	ParentID      ID
	Current, Size int64
}

// ListResult carries counted records; count and records may differ in snapshot.
type ListResult struct {
	Current, Size, Total int64
	Records              []Model
}

// NewCatalog requires a repository and a trusted ID generator.
func NewCatalog(repository CatalogRepository, nextID func() (ID, error)) (*Catalog, error) {
	if repository == nil || nilValue(repository) || nextID == nil {
		return nil, errors.New("category repository and ID generator required")
	}
	return &Catalog{repository: repository, nextID: nextID}, nil
}
func nilValue(v any) bool {
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}

// Service exposes detail and patch using the same repository.
func (c *Catalog) Service() Service { return Service{Repository: c.repository} }
func (c *Catalog) parentCheck(ctx context.Context, actor string) func(string) error {
	return func(owner string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if actor != owner {
			return ErrForbidden
		}
		return nil
	}
}

// Create stores a category owned by the trusted actor under an authorized book.
func (c *Catalog) Create(ctx context.Context, actor string, parentID ID, name string) (Model, error) {
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	if err := input(actor, parentID); err != nil {
		return Model{}, err
	}
	if err := ValidateName(name); err != nil {
		return Model{}, err
	}
	id, err := c.nextID()
	if err != nil {
		return Model{}, err
	}
	if !id.Valid() {
		return Model{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	m, err := c.repository.Create(ctx, Model{ID: id, ParentID: parentID, Owner: actor, Name: name}, c.parentCheck(ctx, actor))
	if canceled := ctx.Err(); canceled != nil {
		return Model{}, errors.Join(err, canceled)
	}
	return m, err
}

// List checks parent ownership and requests a bounded page in ascending ID order.
func (c *Catalog) List(ctx context.Context, actor string, q ListQuery) (ListResult, error) {
	if err := ctx.Err(); err != nil {
		return ListResult{}, err
	}
	if err := input(actor, q.ParentID); err != nil {
		return ListResult{}, err
	}
	if q.Current < 1 || q.Size < 1 || q.Size > 100 || q.Current-1 > 1_000_000/q.Size {
		return ListResult{}, ErrInvalid
	}
	rows, total, err := c.repository.List(ctx, q.ParentID, (q.Current-1)*q.Size, q.Size, c.parentCheck(ctx, actor))
	if canceled := ctx.Err(); canceled != nil {
		return ListResult{}, errors.Join(err, canceled)
	}
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Current: q.Current, Size: q.Size, Total: total, Records: rows}, nil
}

// Delete atomically checks ownership and system protection before removal.
func (c *Catalog) Delete(ctx context.Context, actor string, id ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := input(actor, id); err != nil {
		return err
	}
	err := c.repository.Delete(ctx, id, func(m Model) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := permitted(actor, m); err != nil {
			return err
		}
		if m.System {
			return ErrImmutable
		}
		return nil
	})
	if canceled := ctx.Err(); canceled != nil {
		return errors.Join(err, canceled)
	}
	return err
}
