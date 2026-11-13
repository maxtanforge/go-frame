package entdb

import (
	"context"
	"errors"

	"github.com/maxtanforge/go-frame/internal/isnil"
	"github.com/maxtanforge/go-frame/pagination"
)

// ErrQuery reports an absent query or a nil clone, not a database execution error.
var ErrQuery = errors.New("entdb: query and its clones must not be nil")

// SliceQuery is implemented by Ent's generated entity query builders.
// Clone must return an independent builder with identical predicates and order.
// Offset and Limit return the receiver; a non-nil builder must never return a
// nil builder from them — the helpers check the clone once, not every step.
type SliceQuery[T any, Q any] interface {
	Clone() Q
	Offset(int) Q
	Limit(int) Q
	All(context.Context) ([]T, error)
}

// PageQuery adds a count operation to the generated query contract.
type PageQuery[T any, Q any] interface {
	SliceQuery[T, Q]
	Count(context.Context) (int, error)
}

// QueryPage counts and fetches independent clones of the same unpaginated query.
// Configure filters and a unique stable order before calling; do not supply a
// pre-existing Limit/Offset, GroupBy or a projection that changes count semantics.
// Queries with ForUpdate, ForShare or SQL modifiers are unsupported: the pinned
// Ent generator's Clone omits those modifiers. Execute locking queries directly
// on the transaction and assemble pagination.NewPage/NewSlice yourself.
// It does not change the supplied builder, start a transaction or guarantee a
// shared database snapshot. Use a transaction/isolation policy when required.
func QueryPage[T any, Q PageQuery[T, Q]](ctx context.Context, query Q, w pagination.Window) (pagination.Page[T], error) {
	var empty pagination.Page[T]
	if err := validateQueryWindow(ctx, query, w, false); err != nil {
		return empty, err
	}
	countQuery := query.Clone()
	if isnil.Check(countQuery) {
		return empty, ErrQuery
	}
	total, err := countQuery.Count(ctx)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if total < 0 {
		return empty, pagination.ErrInvalid
	}
	rows, err := queryRows[T](ctx, query, w, int(w.Size()))
	if err != nil {
		return empty, err
	}
	return pagination.NewPage(rows, w, int64(total))
}

// QuerySlice fetches one additional record without running a count query.
// It has the same query restrictions as QueryPage, including unsupported locking
// and SQL-modifier queries, unpaginated input and a unique stable order.
func QuerySlice[T any, Q SliceQuery[T, Q]](ctx context.Context, query Q, w pagination.Window) (pagination.Slice[T], error) {
	var empty pagination.Slice[T]
	if err := validateQueryWindow(ctx, query, w, true); err != nil {
		return empty, err
	}
	rows, err := queryRows[T](ctx, query, w, int(w.FetchSize()))
	if err != nil {
		return empty, err
	}
	return pagination.NewSlice(rows, w)
}
func validateQueryWindow(ctx context.Context, query any, w pagination.Window, extra bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if isnil.Check(query) {
		return ErrQuery
	}
	if err := w.Validate(); err != nil {
		return err
	}
	maxInt := int64(int(^uint(0) >> 1))
	size := w.Size()
	if extra {
		size = w.FetchSize()
	}
	if w.Offset() > maxInt || size > maxInt {
		return pagination.ErrInvalid
	}
	return nil
}
func queryRows[T any, Q SliceQuery[T, Q]](ctx context.Context, query Q, w pagination.Window, limit int) ([]T, error) {
	q := query.Clone()
	if isnil.Check(q) {
		return nil, ErrQuery
	}
	q = q.Offset(int(w.Offset())).Limit(limit)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
