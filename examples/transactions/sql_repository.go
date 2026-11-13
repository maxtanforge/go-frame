package transactionexample

import (
	"context"
	"database/sql"

	"github.com/maxtanforge/go-frame/database"
)

// SQLRepository uses explicit executors for reads and writes. This example uses
// MySQL placeholders; adapt SQL to the application's chosen database dialect.
type SQLRepository struct{}

// Create inserts a product using the caller-provided executor.
func (SQLRepository) Create(ctx context.Context, exec database.Executor, c CreateCommand) error {
	_, err := exec.ExecContext(ctx, "INSERT INTO products (sku, name) VALUES (?, ?)", c.SKU, c.Name)
	return err
}

// RecordCreated records the event using the same transaction executor.
func (SQLRepository) RecordCreated(ctx context.Context, exec database.Executor, sku string) error {
	_, err := exec.ExecContext(ctx, "INSERT INTO product_events (sku, kind) VALUES (?, ?)", sku, "created")
	return err
}

// SQLTransactions lends one transaction executor to the business callback.
// The application owns DB; repositories must not retain or close the executor.
type SQLTransactions struct{ DB *database.DB }

// WithTx adapts the SQL transaction to this consumer's transaction contract.
func (s SQLTransactions) WithTx(ctx context.Context, opts *sql.TxOptions, fn func(database.Executor) error) error {
	return s.DB.WithTx(ctx, opts, func(tx *sql.Tx) error { return fn(tx) })
}
