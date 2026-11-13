package main

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maxtanforge/go-frame/auth/principal"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	generated "github.com/maxtanforge/go-frame/examples/ent/ent"
	"github.com/stretchr/testify/require"
)

func TestGeneratedGlobalAuditRejectsMissingIdentityBeforeSQL(t *testing.T) {
	pool, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, pool.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	client, err := newClient(pool, "mysql", entdb.Options{}, entdb.MutationOptions{IDs: fixedIDs{}})
	require.NoError(t, err)
	_, err = client.Product.Create().SetName("tea").SetPrice(0).SetActive(false).Save(context.Background())
	require.ErrorIs(t, err, principal.ErrUnauthenticated)
	_, err = client.ProductEvent.Create().SetProductID(42).SetAction("created").Save(context.Background())
	require.ErrorIs(t, err, principal.ErrUnauthenticated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGeneratedIndependentIDAndRuntimeHookOrder(t *testing.T) {
	for _, auditEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "ID only", true: "ID and create audit"}[auditEnabled], func(t *testing.T) {
			pool, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() {
				mock.ExpectClose()
				require.NoError(t, pool.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})
			drv, err := entdb.New(pool, entdb.Options{Dialect: "mysql"})
			require.NoError(t, err)
			client := generated.NewClient(generated.Driver(drv))
			client.ProductEvent.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					event := m.(*generated.ProductEventMutation)
					event.SetID(999)
					event.SetCreatedAt(testNow)
					event.SetCreatedBy("earlier hook")
					event.SetCreatedByKind("system")
					event.SetCreatedByName("")
					return next.Mutate(ctx, m)
				})
			})
			ids, err := entdb.NewIDHook(fixedIDs{})
			require.NoError(t, err)
			hooks := []ent.Hook{ids}
			wantActor := "earlier hook"
			if auditEnabled {
				created, err := entdb.NewAuditHook(entdb.AuditOptions{Profile: entdb.AuditCreateOnly, Actor: exampleActor, Now: func() time.Time { return testNow }})
				require.NoError(t, err)
				hooks = append(hooks, created)
				wantActor = systemActor
			}
			require.NoError(t, entdb.InstallHooks(hooks, client.ProductEvent))
			mock.ExpectExec(exactSQL("INSERT INTO `product_events` (`created_at`, `created_by`, `created_by_kind`, `created_by_name`, `product_id`, `action`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?)")).
				WithArgs(testNow, wantActor, "system", "", int64(456), "created", int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
			event, err := client.ProductEvent.Create().SetProductID(456).SetAction("created").Save(context.Background())
			require.NoError(t, err)
			require.Equal(t, int64(123), event.ID)
			require.Equal(t, wantActor, event.CreatedBy)
		})
	}
}
