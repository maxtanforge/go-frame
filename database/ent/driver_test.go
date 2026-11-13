package entdb_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	framelog "github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/stretchr/testify/require"
)

func TestDriverBorrowsPool(t *testing.T) {
	for _, name := range []string{dialect.MySQL, dialect.Postgres} {
		t.Run(name, func(t *testing.T) {
			pool, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			drv, err := entdb.New(pool, entdb.Options{Dialect: name})
			require.NoError(t, err)
			require.Equal(t, name, drv.Dialect())
			require.NoError(t, drv.Close())
			require.NoError(t, drv.Close())
			mock.ExpectPing()
			require.NoError(t, pool.Ping())
			mock.ExpectClose()
			require.NoError(t, pool.Close())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestDriverInvalidOptions(t *testing.T) {
	_, err := entdb.New(nil, entdb.Options{Dialect: dialect.MySQL})
	require.Error(t, err)
	pool, m, err := sqlmock.New()
	require.NoError(t, err)
	for _, opts := range []entdb.Options{{}, {Dialect: "sqlite3"}, {Dialect: dialect.MySQL, SlowThreshold: -time.Second}} {
		_, err = entdb.New(pool, opts)
		require.Error(t, err)
	}
	m.ExpectClose()
	require.NoError(t, pool.Close())
	require.NoError(t, m.ExpectationsWereMet())
}

func TestDriverDirectQueries(t *testing.T) {
	for _, name := range []string{dialect.MySQL, dialect.Postgres} {
		t.Run(name, func(t *testing.T) {
			pool, m, err := sqlmock.New()
			require.NoError(t, err)
			var logs bytes.Buffer
			drv, err := entdb.New(pool, entdb.Options{Dialect: name, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
			require.NoError(t, err)
			m.ExpectExec("UPDATE records").WithArgs(7).WillReturnResult(sqlmock.NewResult(0, 1))
			var result sql.Result
			require.NoError(t, drv.Exec(context.Background(), "UPDATE records SET value=?", []any{7}, &result))
			affected, err := result.RowsAffected()
			require.NoError(t, err)
			require.Equal(t, int64(1), affected)
			m.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
			var rows entsql.Rows
			require.NoError(t, drv.Query(context.Background(), "SELECT id", []any{}, &rows))
			require.True(t, rows.Next())
			var id int
			require.NoError(t, rows.Scan(&id))
			require.Equal(t, 7, id)
			require.NoError(t, rows.Close())
			failure := errors.New("sensitive connection failure")
			m.ExpectBegin().WillReturnError(failure)
			_, err = drv.Tx(context.Background())
			require.ErrorIs(t, err, failure)
			require.Contains(t, logs.String(), `"operation":"begin"`)
			require.NotContains(t, logs.String(), failure.Error())
			m.ExpectClose()
			require.NoError(t, pool.Close())
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestDriverTransactionLogsAndCancellation(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			pool, m, err := sqlmock.New()
			require.NoError(t, err)
			var fallback, logs bytes.Buffer
			drv, err := entdb.New(pool, entdb.Options{Dialect: dialect.MySQL, Logger: slog.New(slog.NewJSONHandler(&fallback, nil)), SlowThreshold: time.Nanosecond})
			require.NoError(t, err)
			ctx := framelog.IntoContext(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
			m.ExpectBegin()
			tx, err := drv.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
			require.NoError(t, err)
			m.ExpectExec("UPDATE secrets").WithArgs("private-value").WillReturnResult(sqlmock.NewResult(0, 1))
			require.NoError(t, tx.Exec(ctx, "UPDATE secrets SET password=?", []any{"private-value"}, nil))
			failure := errors.New("private-driver-error")
			m.ExpectQuery("SELECT secret").WillReturnError(failure)
			var rows entsql.Rows
			require.ErrorIs(t, tx.Query(ctx, "SELECT secret", []any{}, &rows), failure)
			if rollback {
				m.ExpectRollback()
				require.NoError(t, tx.Rollback())
			} else {
				m.ExpectCommit()
				require.NoError(t, tx.Commit())
			}
			require.Contains(t, logs.String(), "duration_ms")
			require.Contains(t, logs.String(), `"failed":true`)
			require.Contains(t, logs.String(), `"operation":"query"`)
			for _, s := range []string{"private-value", "private-driver-error", "secrets", "password", "SELECT"} {
				require.NotContains(t, logs.String(), s)
			}
			require.Empty(t, fallback.String())
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = drv.Tx(canceled)
			require.ErrorIs(t, err, context.Canceled)
			m.ExpectClose()
			require.NoError(t, pool.Close())
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestCanceledEntOperationDoesNotLogAsFailure(t *testing.T) {
	pool, mock, err := sqlmock.New()
	require.NoError(t, err)
	var logs bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	drv, err := entdb.New(pool, entdb.Options{Dialect: dialect.MySQL, Logger: lg})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = drv.Tx(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, logs.String(), `"level":"DEBUG"`)
	require.Contains(t, logs.String(), `"canceled":true`)
	require.NotContains(t, logs.String(), `"level":"ERROR"`)
	mock.ExpectClose()
	require.NoError(t, pool.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestJoinedCancellationAndDatabaseFailureStillLogsError(t *testing.T) {
	pool, mock, err := sqlmock.New()
	require.NoError(t, err)
	var logs bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	drv, err := entdb.New(pool, entdb.Options{Dialect: dialect.MySQL, Logger: lg})
	require.NoError(t, err)
	databaseFailure := errors.New("private database failure")
	mock.ExpectExec("UPDATE things").WillReturnError(errors.Join(context.Canceled, databaseFailure))
	err = drv.Exec(context.Background(), "UPDATE things SET value=1", []any{}, nil)
	require.ErrorIs(t, err, databaseFailure)
	require.Contains(t, logs.String(), `"level":"ERROR"`)
	require.Contains(t, logs.String(), `"failed":true`)
	require.NotContains(t, logs.String(), databaseFailure.Error())
	mock.ExpectClose()
	require.NoError(t, pool.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}
