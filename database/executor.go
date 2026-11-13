package database

import (
	"context"
	"database/sql"
)

// Executor is the common query surface of *sql.DB and *sql.Tx. Repositories
// receive it explicitly so every operation in a transaction uses the same executor.
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var _ Executor = (*sql.DB)(nil)
var _ Executor = (*sql.Tx)(nil)
