// Package transactionexample demonstrates explicit business transactions and typed HTTP boundaries.
package transactionexample

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/maxtanforge/go-frame/database"
)

// ErrInvalidProduct identifies a rejected business command.
var ErrInvalidProduct = errors.New("invalid product")

// CreateCommand is a business input, independent of HTTP DTO tags.
type CreateCommand struct{ SKU, Name string }

// Product is a business read model.
type Product struct{ SKU, Name string }

// ListQuery describes a business query independent of HTTP defaults.
type ListQuery struct {
	Category string
	Limit    int
}

// Transactions is the capability needed by this use case.
type Transactions interface {
	WithTx(context.Context, *sql.TxOptions, func(database.Executor) error) error
}

// ProductStore borrows the executor for each operation; it owns no pool.
type ProductStore interface {
	Create(context.Context, database.Executor, CreateCommand) error
	RecordCreated(context.Context, database.Executor, string) error
}

// Service protects business invariants for HTTP and non-HTTP callers alike.
type Service struct {
	Transactions Transactions
	Store        ProductStore
}

// Create validates the command and atomically stores the product and its event.
func (s Service) Create(ctx context.Context, c CreateCommand) error {
	if strings.TrimSpace(c.SKU) == "" || strings.TrimSpace(c.Name) == "" {
		return ErrInvalidProduct
	}
	return s.Transactions.WithTx(ctx, nil, func(exec database.Executor) error {
		if err := s.Store.Create(ctx, exec, c); err != nil {
			return err
		}
		return s.Store.RecordCreated(ctx, exec, c.SKU)
	})
}
