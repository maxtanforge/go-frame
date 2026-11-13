package entdb_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"testing"

	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/stretchr/testify/require"
)

type testTx struct {
	commits, rollbacks     int
	commitErr, rollbackErr error
}

func (tx *testTx) Commit() error   { tx.commits++; return tx.commitErr }
func (tx *testTx) Rollback() error { tx.rollbacks++; return tx.rollbackErr }

func TestWithTxOutcomes(t *testing.T) {
	beginErr, callbackErr, commitErr, rollbackErr := errors.New("begin"), errors.New("callback"), errors.New("commit"), errors.New("rollback")
	for _, tt := range []struct {
		name                                          string
		beginErr, callbackErr, commitErr, rollbackErr error
		calls, commits, rollbacks                     int
	}{
		{name: "success", calls: 1, commits: 1},
		{name: "begin failure", beginErr: beginErr},
		{name: "callback failure", callbackErr: callbackErr, calls: 1, rollbacks: 1},
		{name: "joined rollback failure", callbackErr: callbackErr, rollbackErr: rollbackErr, calls: 1, rollbacks: 1},
		{name: "uncertain commit", commitErr: commitErr, calls: 1, commits: 1, rollbacks: 1},
		{name: "commit and cleanup failure", commitErr: commitErr, rollbackErr: rollbackErr, calls: 1, commits: 1, rollbacks: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := &testTx{commitErr: tt.commitErr, rollbackErr: tt.rollbackErr}
			calls := 0
			err := entdb.WithTx(context.Background(), func(context.Context) (*testTx, error) { return tx, tt.beginErr }, func(got *testTx) error { require.Same(t, tx, got); calls++; return tt.callbackErr })
			for _, want := range []error{tt.beginErr, tt.callbackErr, tt.commitErr, tt.rollbackErr} {
				if want != nil {
					require.ErrorIs(t, err, want)
				}
			}
			if tt.beginErr == nil && tt.callbackErr == nil && tt.commitErr == nil {
				require.NoError(t, err)
			}
			require.Equal(t, tt.calls, calls)
			require.Equal(t, tt.commits, tx.commits)
			require.Equal(t, tt.rollbacks, tx.rollbacks)
		})
	}
}

func TestWithTxCompletedTransactionCleanup(t *testing.T) {
	failure := errors.New("post commit hook failed")
	for _, rollbackErr := range []error{sql.ErrTxDone, fmt.Errorf("wrapped: %w", sql.ErrTxDone)} {
		tx := &testTx{commitErr: failure, rollbackErr: rollbackErr}
		err := entdb.WithTx(context.Background(), func(context.Context) (*testTx, error) { return tx, nil }, func(*testTx) error { return nil })
		require.ErrorIs(t, err, failure)
		require.Equal(t, 1, tx.rollbacks)
		require.Same(t, failure, err)
	}
}

func TestWithTxRetainsMixedRollbackErrors(t *testing.T) {
	failure := errors.New("post commit hook failed")
	cleanup := errors.New("rollback hook failed")
	for _, rollbackErr := range []error{
		errors.Join(sql.ErrTxDone, cleanup),
		fmt.Errorf("wrapped: %w", errors.Join(sql.ErrTxDone, cleanup)),
	} {
		tx := &testTx{commitErr: failure, rollbackErr: rollbackErr}
		err := entdb.WithTx(context.Background(), func(context.Context) (*testTx, error) { return tx, nil }, func(*testTx) error { return nil })
		require.ErrorIs(t, err, failure)
		require.ErrorIs(t, err, cleanup)
		require.Equal(t, 1, tx.rollbacks)
	}
}

func TestWithTxRetainsCallbackFailureAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("business operation failed")
	cleanup := errors.New("rollback hook failed")
	tx := &testTx{rollbackErr: cleanup}
	err := entdb.WithTx(ctx, func(context.Context) (*testTx, error) { return tx, nil }, func(*testTx) error {
		cancel()
		return failure
	})
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, cleanup)
	require.Equal(t, 1, tx.rollbacks)
	require.Zero(t, tx.commits)
}

func TestWithTxCancellationPreservesSingleCause(t *testing.T) {
	for _, callbackErr := range []error{nil, context.Canceled, fmt.Errorf("callback: %w", context.Canceled)} {
		ctx, cancel := context.WithCancel(context.Background())
		tx := &testTx{}
		err := entdb.WithTx(ctx, func(context.Context) (*testTx, error) { return tx, nil }, func(*testTx) error {
			cancel()
			return callbackErr
		})
		cancel()
		want := callbackErr
		if want == nil {
			want = context.Canceled
		}
		require.Same(t, want, err)
		require.Equal(t, 1, tx.rollbacks)
		require.Zero(t, tx.commits)
	}
}

func TestWithTxInvalidInputs(t *testing.T) {
	called := false
	begin := func(context.Context) (*testTx, error) { called = true; return &testTx{}, nil }
	fn := func(*testTx) error { t.Fatal("callback called"); return nil }
	require.Error(t, entdb.WithTx[*testTx](context.Background(), nil, fn))
	require.Error(t, entdb.WithTx(context.Background(), begin, nil))
	//lint:ignore SA1012 This negative test verifies rejection of a nil context.
	require.Error(t, entdb.WithTx(nil, begin, fn)) //nolint:staticcheck // 负向测试:显式验证 nil context 被拒绝
	require.False(t, called)
	require.Error(t, entdb.WithTx(context.Background(), func(context.Context) (*testTx, error) { return nil, nil }, fn))
	require.Error(t, entdb.WithTx(context.Background(), func(context.Context) (entdb.Transaction, error) { var tx *testTx; return tx, nil }, func(entdb.Transaction) error { t.Fatal("callback called"); return nil }))
}

func TestWithTxCancellation(t *testing.T) {
	for _, stage := range []string{"before begin", "during begin", "during callback"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx := &testTx{rollbackErr: errors.New("rollback")}
			begins, calls := 0, 0
			if stage == "before begin" {
				cancel()
			}
			err := entdb.WithTx(ctx, func(context.Context) (*testTx, error) {
				begins++
				if stage == "during begin" {
					cancel()
				}
				return tx, nil
			}, func(*testTx) error { calls++; cancel(); return nil })
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, tx.commits)
			if stage == "before begin" {
				require.Zero(t, begins)
				require.Zero(t, tx.rollbacks)
			} else {
				require.Equal(t, 1, tx.rollbacks)
				require.ErrorIs(t, err, tx.rollbackErr)
			}
			if stage != "during callback" {
				require.Zero(t, calls)
			}
		})
	}
}

func TestWithTxPanicAndGoexit(t *testing.T) {
	t.Run("panic", func(t *testing.T) {
		tx := &testTx{}
		marker := &struct{}{}
		require.PanicsWithValue(t, marker, func() {
			_ = entdb.WithTx(context.Background(), func(context.Context) (*testTx, error) { return tx, nil }, func(*testTx) error { panic(marker) })
		})
		require.Equal(t, 1, tx.rollbacks)
		require.Zero(t, tx.commits)
	})
	t.Run("goexit", func(t *testing.T) {
		tx := &testTx{}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = entdb.WithTx(context.Background(), func(context.Context) (*testTx, error) { return tx, nil }, func(*testTx) error { runtime.Goexit(); return nil })
		}()
		<-done
		require.Equal(t, 1, tx.rollbacks)
		require.Zero(t, tx.commits)
	})
}
