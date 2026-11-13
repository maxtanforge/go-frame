package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

func TestPostgresFilteredPage(t *testing.T) {
	pool, m, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { m.ExpectClose(); require.NoError(t, pool.Close()); require.NoError(t, m.ExpectationsWereMet()) })
	client, err := newClient(pool, "postgres", entdb.Options{}, entdb.MutationOptions{IDs: fixedIDs{}, Actor: exampleActor, Now: func() time.Time { return testNow }})
	require.NoError(t, err)
	service, err := NewService(client)
	require.NoError(t, err)
	m.ExpectQuery(exactSQL(`SELECT COUNT("products"."id") FROM "products" WHERE NOT "products"."active"`)).WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	m.ExpectQuery(`SELECT .* FROM "products" WHERE NOT "products"."active" ORDER BY "products"."price" DESC, "products"."id" LIMIT 2`).WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"id", "price", "active"}).AddRow(1, 90, false).AddRow(2, 80, false))
	active := false
	result, err := service.List(context.Background(), ListProducts{Input: pagination.Input{Current: 1, Size: 2}, Active: &active, Sort: "price", Desc: true})
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Total)
	require.Len(t, result.Records, 2)
}

// SQL generation tests are not PostgreSQL live verification.
func TestPostgresCreateTransaction(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			pool, m, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { m.ExpectClose(); require.NoError(t, pool.Close()); require.NoError(t, m.ExpectationsWereMet()) })
			client, err := newClient(pool, "postgres", entdb.Options{}, entdb.MutationOptions{IDs: fixedIDs{}, Actor: exampleActor, Now: func() time.Time { return testNow }})
			require.NoError(t, err)
			s, err := NewService(client)
			require.NoError(t, err)
			m.ExpectBegin()
			m.ExpectExec(exactSQL(`INSERT INTO "products" ("created_at", "updated_at", "created_by", "updated_by", "created_by_kind", "updated_by_kind", "created_by_name", "updated_by_name", "name", "price", "active", "id") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`)).WithArgs(testNow, testNow, systemActor, systemActor, "system", "system", "", "", "tea", int64(0), false, int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
			event := m.ExpectExec(exactSQL(`INSERT INTO "product_events" ("created_at", "created_by", "created_by_kind", "created_by_name", "product_id", "action", "id") VALUES ($1, $2, $3, $4, $5, $6, $7)`)).WithArgs(testNow, systemActor, "system", "", int64(123), "created", int64(123))
			failure := errors.New("event failed")
			if fail {
				event.WillReturnError(failure)
				m.ExpectRollback()
			} else {
				event.WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit()
			}
			p, err := s.Create(context.Background(), CreateProduct{Name: "tea"})
			if fail {
				require.ErrorIs(t, err, failure)
				require.Nil(t, p)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(123), p.ID)
			}
		})
	}
}
