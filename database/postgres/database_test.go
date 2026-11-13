package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/internal/sqltest"
	"github.com/stretchr/testify/require"
)

func TestOpenLazyAndValidation(t *testing.T) {
	// Unit tests must not depend on the developer's libpq environment. The
	// opt-in integration test intentionally retains it for real connections.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "PG") {
			t.Setenv(key, "")
		}
	}
	for _, dsn := range []string{"", "postgres://user:secret@127.0.0.1:1/db?sslmode=secret", "password=secret invalid"} {
		db, err := Open(Options{DSN: dsn})
		require.Error(t, err)
		require.Nil(t, db)
		require.NotContains(t, err.Error(), "secret")
	}
	_, err := Open(Options{DSN: "postgres://user:secret@127.0.0.1:1/db?sslmode=disable", Pool: database.Options{MaxOpenConns: -1}})
	require.Error(t, err)
	db, err := Open(Options{DSN: "postgres://user:secret@127.0.0.1:1/db?sslmode=disable", Pool: database.Options{MaxOpenConns: 2}})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.Equal(t, 2, db.SQLDB().Stats().MaxOpenConnections)
	require.Zero(t, db.SQLDB().Stats().OpenConnections)
	keywordDB, err := Open(Options{DSN: "host=127.0.0.1 port=1 user=test password=secret dbname=test sslmode=disable"})
	require.NoError(t, err)
	defer func() { _ = keywordDB.Close() }()
	require.Zero(t, keywordDB.SQLDB().Stats().OpenConnections)
}
func TestIntegration(t *testing.T) {
	dsn := os.Getenv("FRAME_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set FRAME_TEST_POSTGRES_DSN to opt in")
	}
	db, err := Open(Options{DSN: dsn})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, db.Ping(ctx))
	abort := errors.New("intentional rollback")
	for _, callbackErr := range []error{nil, abort} {
		var n int
		err = db.WithTx(ctx, nil, func(tx *sql.Tx) error {
			if err := tx.QueryRowContext(ctx, "SELECT $1::int", 42).Scan(&n); err != nil {
				return err
			}
			return callbackErr
		})
		if callbackErr == nil {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, abort)
		}
		require.Equal(t, 42, n)
	}
	require.NoError(t, db.Ping(ctx))
	sqltest.Exercise(t, db, true)
}
