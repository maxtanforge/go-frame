package main

import (
	"context"
	"errors"
	"runtime"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	generated "github.com/maxtanforge/go-frame/examples/ent/ent"
	"github.com/stretchr/testify/require"
)

// This is a consumer-level integration test: it exercises entdb.WithTx against
// the generated commit-hook machinery (hooks can fail before the driver
// commits), which only exists in generated clients. The framework package
// covers its own contract with fakes in database/ent/transaction_test.go.

func TestWithTxGeneratedCommitHooksReleaseConnection(t *testing.T) {
	for _, mode := range []string{"veto", "panic", "goexit", "after commit", "driver error"} {
		t.Run(mode, func(t *testing.T) {
			pool, m, err := sqlmock.New()
			require.NoError(t, err)
			client := generated.NewClient(generated.Driver(entsql.OpenDB("mysql", pool)))
			var saved *generated.Tx
			t.Cleanup(func() {
				if saved != nil && pool.Stats().InUse != 0 {
					_ = saved.Rollback()
				}
				m.ExpectClose()
				require.NoError(t, pool.Close())
				require.NoError(t, m.ExpectationsWereMet())
			})
			failure := errors.New("commit hook failure")
			m.ExpectBegin()
			switch mode {
			case "after commit":
				m.ExpectCommit()
			case "driver error":
				m.ExpectCommit().WillReturnError(failure)
			default:
				m.ExpectRollback()
			}
			run := func() error {
				return entdb.WithTx(context.Background(), client.Tx, func(tx *generated.Tx) error {
					saved = tx
					tx.OnCommit(func(next generated.Committer) generated.Committer {
						return generated.CommitFunc(func(ctx context.Context, tx *generated.Tx) error {
							switch mode {
							case "panic":
								panic(failure)
							case "goexit":
								runtime.Goexit()
							case "driver error":
								return next.Commit(ctx, tx)
							case "after commit":
								if err := next.Commit(ctx, tx); err != nil {
									return err
								}
							}
							return failure
						})
					})
					return nil
				})
			}
			switch mode {
			case "panic":
				require.PanicsWithValue(t, failure, func() { _ = run() })
			case "goexit":
				done := make(chan struct{})
				go func() { defer close(done); _ = run() }()
				<-done
			default:
				require.ErrorIs(t, run(), failure)
			}
			require.Zero(t, pool.Stats().InUse, "transaction must release the borrowed connection")
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
