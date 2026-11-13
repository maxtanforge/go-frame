// Package sqltest contains opt-in database integration assertions for adapters.
package sqltest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"github.com/maxtanforge/go-frame/database"
	"strings"
	"testing"
	"time"
)

// Exercise creates only a uniquely named test table. Use a disposable test database
// with CREATE/DROP permissions; cleanup never touches application tables.
func Exercise(t *testing.T, db *database.DB, postgres bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	table := "frame_test_" + strings.ToLower(rand.Text())
	ddl := "CREATE TABLE " + table + " (id BIGINT PRIMARY KEY, payload VARCHAR(80) NOT NULL)"
	if !postgres {
		ddl += " ENGINE=InnoDB"
	}
	if _, err := db.SQLDB().ExecContext(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := db.SQLDB().ExecContext(cleanup, "DROP TABLE "+table); err != nil {
			t.Errorf("cleanup test table %s: %v", table, err)
		}
	}()
	insert := "INSERT INTO " + table + " (id,payload) VALUES (?,?)"
	if postgres {
		insert = "INSERT INTO " + table + " (id,payload) VALUES ($1,$2)"
	}
	rollback := errors.New("intentional rollback")
	for _, id := range []int{1, 2} {
		err := db.WithTx(ctx, nil, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, insert, id, "test value"); err != nil {
				return err
			}
			if id == 2 {
				return rollback
			}
			return nil
		})
		if id == 1 && err != nil {
			t.Fatal(err)
		}
		if id == 2 && !errors.Is(err, rollback) {
			t.Fatalf("expected rollback result, got %v", err)
		}
	}
	var count int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("commit/rollback visibility: got %d rows", count)
	}
	var value string
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT payload FROM "+table+" WHERE id=1").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "test value" {
		t.Fatal("committed value mismatch")
	}
	exercisePagination(ctx, t, db, table, insert, postgres)
	exerciseAudit(ctx, t, db, table, postgres)
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := db.Ping(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ping: %v", err)
	}
	previous := db.SQLDB().Stats().MaxOpenConnections
	db.SQLDB().SetMaxOpenConns(1)
	defer db.SQLDB().SetMaxOpenConns(previous)
	held, err := db.SQLDB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	waitCtx, stopWait := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stopWait()
	if err := db.Ping(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exhausted pool must honor deadline: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("pool did not recover: %v", err)
	}
}
