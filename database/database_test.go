package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenValidationAndPool(t *testing.T) {
	for _, o := range []Options{{MaxOpenConns: -1}, {MaxIdleConns: -1}, {ConnMaxIdleTime: -time.Second}, {ConnMaxLifetime: -time.Second}, {MaxOpenConns: 2, MaxIdleConns: 3}} {
		_, err := Open(txConnector{&txState{}}, o)
		require.Error(t, err)
	}
	for _, connector := range []driver.Connector{nil, (*txConnector)(nil), nilConnector(nil)} {
		_, err := Open(connector, Options{})
		require.Error(t, err)
	}
	db, err := Open(txConnector{&txState{}}, Options{})
	require.NoError(t, err)
	require.Equal(t, 25, db.SQLDB().Stats().MaxOpenConnections)
	require.Zero(t, db.SQLDB().Stats().OpenConnections)
	require.NoError(t, db.Close())
	require.NoError(t, db.Close())
	db, err = Open(txConnector{&txState{}}, Options{MaxOpenConns: 2})
	require.NoError(t, err)
	require.Equal(t, 2, db.SQLDB().Stats().MaxOpenConnections)
	require.NoError(t, db.Close())
	var absent *DB
	require.Nil(t, absent.SQLDB())
	require.Error(t, absent.Ping(context.Background()))
	require.NoError(t, absent.Close())
	require.Error(t, (&DB{}).Ping(context.Background()))
}

// A connector need not have a pointer concrete type.
type nilConnector func()

func (nilConnector) Connect(context.Context) (driver.Conn, error) { panic("must not connect") }
func (nilConnector) Driver() driver.Driver                        { return txDriver{} }

type ownedConnector struct {
	connects, closes int
	err              error
}

func (c *ownedConnector) Connect(context.Context) (driver.Conn, error) {
	c.connects++
	return nil, errors.New("test connection unavailable")
}
func (c *ownedConnector) Driver() driver.Driver { return txDriver{} }
func (c *ownedConnector) Close() error          { c.closes++; return c.err }

func TestOpenConnectorOwnership(t *testing.T) {
	c := &ownedConnector{err: errors.New("close failed")}
	_, err := Open(c, Options{MaxOpenConns: -1})
	require.Error(t, err)
	require.Zero(t, c.connects)
	require.Zero(t, c.closes)
	db, err := Open(c, Options{})
	require.NoError(t, err)
	require.Zero(t, c.connects)
	require.ErrorIs(t, db.Close(), c.err)
	require.ErrorIs(t, db.Close(), c.err)
	require.Equal(t, 1, c.closes)
}

type txState struct {
	begins, commits, rollbacks       int
	beginErr, commitErr, rollbackErr error
	beginHook                        func()
	rollbackHook                     func()
	options                          driver.TxOptions
}
type txConnector struct{ state *txState }

func (c txConnector) Connect(context.Context) (driver.Conn, error) { return &txConn{c.state}, nil }
func (c txConnector) Driver() driver.Driver                        { return txDriver{} }

type txDriver struct{}

func (txDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type txConn struct{ state *txState }

func (c *txConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *txConn) Close() error                        { return nil }
func (c *txConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *txConn) BeginTx(_ context.Context, o driver.TxOptions) (driver.Tx, error) {
	c.state.begins++
	if c.state.beginHook != nil {
		c.state.beginHook()
	}
	c.state.options = o
	return &fakeTx{c.state}, c.state.beginErr
}

type fakeTx struct{ state *txState }

func (t *fakeTx) Commit() error { t.state.commits++; return t.state.commitErr }
func (t *fakeTx) Rollback() error {
	t.state.rollbacks++
	if t.state.rollbackHook != nil {
		t.state.rollbackHook()
	}
	return t.state.rollbackErr
}

func TestWithTx(t *testing.T) {
	sentinel := errors.New("callback")
	rollbackErr := errors.New("rollback")
	beginErr := errors.New("begin")
	commitErr := errors.New("commit")
	for _, tc := range []struct {
		name               string
		state              txState
		callbackErr        error
		wants              []error
		commits, rollbacks int
	}{
		{name: "commit", commits: 1}, {name: "callback rollback", callbackErr: sentinel, wants: []error{sentinel}, rollbacks: 1},
		{name: "rollback error", state: txState{rollbackErr: rollbackErr}, callbackErr: sentinel, wants: []error{sentinel, rollbackErr}, rollbacks: 1},
		{name: "mixed completed rollback error", state: txState{rollbackErr: errors.Join(sql.ErrTxDone, rollbackErr)}, callbackErr: sentinel, wants: []error{sentinel, rollbackErr}, rollbacks: 1},
		{name: "begin error", state: txState{beginErr: beginErr}, wants: []error{beginErr}},
		{name: "commit error", state: txState{commitErr: commitErr}, wants: []error{commitErr}, commits: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &DB{sqlDB: sql.OpenDB(txConnector{&tc.state})}
			defer func() { _ = db.Close() }()
			called := false
			err := db.WithTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true}, func(tx *sql.Tx) error { called = true; require.NotNil(t, tx); return tc.callbackErr })
			if len(tc.wants) == 0 {
				require.NoError(t, err)
			}
			for _, want := range tc.wants {
				require.ErrorIs(t, err, want)
			}
			require.Equal(t, tc.state.beginErr == nil, called)
			require.Equal(t, tc.commits, tc.state.commits)
			require.Equal(t, tc.rollbacks, tc.state.rollbacks)
			require.Equal(t, driver.TxOptions{Isolation: driver.IsolationLevel(sql.LevelSerializable), ReadOnly: true}, tc.state.options)
		})
	}
}
func TestWithTxCancellationPreservesSingleCause(t *testing.T) {
	for _, callbackErr := range []error{nil, context.Canceled, fmt.Errorf("callback: %w", context.Canceled)} {
		ctx, cancel := context.WithCancel(context.Background())
		db := &DB{sqlDB: sql.OpenDB(txConnector{&txState{}})}
		err := db.WithTx(ctx, nil, func(*sql.Tx) error {
			cancel()
			return callbackErr
		})
		cancel()
		require.NoError(t, db.Close())
		want := callbackErr
		if want == nil {
			want = context.Canceled
		}
		require.Same(t, want, err)
	}
}

func TestWithTxPanicRollsBack(t *testing.T) {
	s := &txState{}
	db := &DB{sqlDB: sql.OpenDB(txConnector{s})}
	defer func() { _ = db.Close() }()
	require.PanicsWithValue(t, "boom", func() { _ = db.WithTx(context.Background(), nil, func(*sql.Tx) error { panic("boom") }) })
	require.Equal(t, 1, s.rollbacks)
}

func TestWithTxGoexitRollsBack(t *testing.T) {
	s := &txState{}
	db := &DB{sqlDB: sql.OpenDB(txConnector{s})}
	defer func() { _ = db.Close() }()
	db.SQLDB().SetMaxOpenConns(1)
	var startedTx *sql.Tx
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = db.WithTx(context.Background(), nil, func(tx *sql.Tx) error {
			startedTx = tx
			runtime.Goexit()
			return nil
		})
	}()
	<-done
	require.NotNil(t, startedTx)
	defer func() { _ = startedTx.Rollback() }() // Release the connection even if the regression fails.
	require.Equal(t, 0, s.commits)
	require.Equal(t, 1, s.rollbacks)
	require.Zero(t, db.SQLDB().Stats().InUse)
}

func TestWithTxCanceledCallbackReturnsContextCause(t *testing.T) {
	rolledBack := make(chan struct{})
	s := &txState{rollbackHook: func() { close(rolledBack) }}
	db := &DB{sqlDB: sql.OpenDB(txConnector{s})}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := db.WithTx(ctx, nil, func(*sql.Tx) error { cancel(); <-rolledBack; return nil })
	require.ErrorIs(t, err, context.Canceled)
}
func TestWithTxNilContext(t *testing.T) {
	db := &DB{sqlDB: sql.OpenDB(txConnector{&txState{}})}
	defer func() { _ = db.Close() }()
	require.Error(t, db.WithTx(nil, nil, func(*sql.Tx) error { return nil })) //nolint:staticcheck // Deliberately verify nil-context handling.
}

func TestWithTxCancellationDuringBeginSkipsCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &txState{beginHook: cancel}
	db := &DB{sqlDB: sql.OpenDB(txConnector{s})}
	defer func() { _ = db.Close() }()
	err := db.WithTx(ctx, nil, func(*sql.Tx) error { t.Error("callback reached after cancellation"); return nil })
	require.ErrorIs(t, err, context.Canceled)
}
