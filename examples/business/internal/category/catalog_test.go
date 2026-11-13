package category

import (
	"context"
	"errors"
	"testing"
)

type catalogMemory struct {
	model        Model
	parentOwner  string
	calls        int
	offset, size int64
}

func (r *catalogMemory) Find(context.Context, ID) (Model, error) { return r.model, nil }
func (r *catalogMemory) Update(_ context.Context, _ ID, change func(*Model) error) (Model, error) {
	m := r.model
	if err := change(&m); err != nil {
		return Model{}, err
	}
	r.model = m
	return m, nil
}
func (r *catalogMemory) Create(_ context.Context, m Model, check func(string) error) (Model, error) {
	r.calls++
	if err := check(r.parentOwner); err != nil {
		return Model{}, err
	}
	r.model = m
	return m, nil
}
func (r *catalogMemory) List(_ context.Context, _ ID, offset, size int64, check func(string) error) ([]Model, int64, error) {
	r.calls++
	r.offset, r.size = offset, size
	if err := check(r.parentOwner); err != nil {
		return nil, 0, err
	}
	return []Model{r.model}, 1, nil
}
func (r *catalogMemory) Delete(_ context.Context, _ ID, check func(Model) error) error {
	r.calls++
	if err := check(r.model); err != nil {
		return err
	}
	r.model = Model{}
	return nil
}
func TestCatalogRules(t *testing.T) {
	ctx := context.Background()
	parent, _ := NewID(7)
	id, _ := NewID(42)
	r := &catalogMemory{parentOwner: "alice"}
	c, err := NewCatalog(r, func() (ID, error) { return id, nil })
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Create(ctx, "alice", parent, "new")
	if err != nil || m.ID != id || m.ParentID != parent || m.Owner != "alice" || m.System {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err = c.Service().Find(ctx, "alice", id); err != nil {
		t.Fatal(err)
	}
	page, err := c.List(ctx, "alice", ListQuery{ParentID: parent, Current: 2, Size: 20})
	if err != nil || page.Total != 1 || r.offset != 20 || r.size != 20 {
		t.Fatalf("%+v %v", page, err)
	}
	for _, q := range []ListQuery{{parent, 0, 20}, {parent, 1, 101}, {parent, 9223372036854775807, 100}} {
		before := r.calls
		if _, err = c.List(ctx, "alice", q); !errors.Is(err, ErrInvalid) || r.calls != before {
			t.Fatalf("invalid query touched storage: %v", err)
		}
	}
	before := r.calls
	for _, name := range []string{"", " ", "bad\x00", "\xff"} {
		if _, err = c.Create(ctx, "alice", parent, name); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if r.calls != before {
		t.Fatal("invalid name touched storage")
	}
	if _, err = c.Create(ctx, "", parent, "ok"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal(err)
	}
	if _, err = c.Create(ctx, "bob", parent, "ok"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = c.List(ctx, "bob", ListQuery{parent, 1, 20}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	r.model.System = true
	if err = c.Delete(ctx, "alice", id); !errors.Is(err, ErrImmutable) {
		t.Fatal(err)
	}
	if err = c.Delete(ctx, "bob", id); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	r.model.System = false
	if err = c.Delete(ctx, "alice", id); err != nil || r.model.ID.Valid() {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	before = r.calls
	if _, err = c.Create(cancelCtx, "alice", parent, "ok"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = c.List(cancelCtx, "alice", ListQuery{parent, 1, 20}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = c.Delete(cancelCtx, "alice", id); !errors.Is(err, context.Canceled) || r.calls != before {
		t.Fatal(err)
	}
}
func TestCatalogConstructionAndText(t *testing.T) {
	var typedNil *catalogMemory
	next := func() (ID, error) { return NewID(1) }
	for _, r := range []CatalogRepository{nil, typedNil} {
		if _, err := NewCatalog(r, next); err == nil {
			t.Fatal("nil repository accepted")
		}
	}
	if _, err := NewCatalog(&catalogMemory{}, nil); err == nil {
		t.Fatal("nil IDs accepted")
	}
	if !errors.Is(ValidateName(" "), ErrInvalid) || ValidateNote("") != nil || !errors.Is(ValidateNote("\x00"), ErrInvalid) {
		t.Fatal("text rules")
	}
}
