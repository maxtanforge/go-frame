package entdb

import (
	"context"
	"errors"

	"github.com/maxtanforge/go-frame/database/internal/txcleanup"
	"github.com/maxtanforge/go-frame/internal/isnil"
)

// Transaction is the lifecycle implemented by a generated Ent transaction.
type Transaction interface {
	Commit() error
	Rollback() error
}

// WithTx runs fn with the concrete transaction returned by begin. The callback
// must not commit or roll back the transaction itself. No client is injected
// into ctx, and nested transactions and retries are not provided.
//
// Callback errors and context cancellation trigger rollback; a rollback error
// is joined with the original error. Panic and runtime.Goexit also roll back,
// without recovering a panic. Cleanup stays armed until Commit returns nil,
// because Ent commit hooks can fail before the driver commits. On commit failure
// rollback is attempted, without inferring the commit outcome or retrying writes.
// Only single wrapping chains ending in sql.ErrTxDone are ignored during cleanup.
// Compound cleanup errors are retained so independent hook failures are not lost.
// Entities returned from fn may still need their generated Unwrap method.
func WithTx[T Transaction](ctx context.Context, begin func(context.Context) (T, error), fn func(T) error) (err error) {
	if isnil.Check(ctx) || begin == nil || fn == nil {
		return errors.New("entdb: transaction context, begin and callback must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := begin(ctx)
	if err != nil {
		return err
	}
	if isnil.Check(tx) {
		return errors.New("entdb: begin returned a nil transaction")
	}
	committed := false
	defer func() {
		if !committed {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !txcleanup.OnlyCompleted(rollbackErr) {
				err = errors.Join(err, rollbackErr)
			}
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	err = fn(tx)
	if canceled := ctx.Err(); canceled != nil {
		if err == nil {
			err = canceled
		} else if !errors.Is(err, canceled) {
			err = errors.Join(err, canceled)
		}
	}
	if err != nil {
		return err
	}
	err = tx.Commit()
	committed = err == nil
	return err
}
