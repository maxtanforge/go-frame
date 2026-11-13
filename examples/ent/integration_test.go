package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/jackc/pgx/v5/pgconn"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/database/query"
	"github.com/maxtanforge/go-frame/examples/ent/ent"
	"github.com/maxtanforge/go-frame/examples/ent/ent/productevent"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/optional"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

// This test pins one physical connection. Every fixture is session-local and
// created before any service SQL. It never migrates or touches persistent tables.
func TestIntegrationEnt(t *testing.T) {
	for _, engine := range []string{dialect.MySQL, dialect.Postgres} {
		t.Run(engine, func(t *testing.T) {
			key := "FRAME_TEST_MYSQL_DSN"
			if engine == dialect.Postgres {
				key = "FRAME_TEST_POSTGRES_DSN"
			}
			dsn := os.Getenv(key)
			if dsn == "" {
				t.Skip(key + " is not configured")
			}
			pool, err := openDatabase(appconfig.DatabaseConfig{Driver: engine, URL: integrationDSN(t, query.Dialect(engine), dsn)})
			integrationOK(t, "open", err)
			t.Cleanup(func() { integrationOK(t, "close pool", pool.Close()) })
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			conn, err := pool.SQLDB().Conn(ctx)
			integrationOK(t, "pin connection", err)
			t.Cleanup(func() { integrationOK(t, "close connection", conn.Close()) })
			if engine == dialect.Postgres {
				_, err = conn.ExecContext(ctx, "SET search_path TO pg_temp")
				integrationOK(t, "isolate schema", err)
			}
			ddl, err := os.ReadFile("migrations/" + engine + "/202609300001_initial.sql")
			integrationOK(t, "read migration", err)
			for _, statement := range strings.Split(string(ddl), ";") {
				statement = strings.TrimSpace(statement)
				if statement == "" {
					continue
				}
				require.True(t, strings.HasPrefix(statement, "CREATE TABLE "), "fixtures permit only table creation")
				statement = strings.Replace(statement, "CREATE TABLE ", "CREATE TEMPORARY TABLE ", 1)
				_, err = conn.ExecContext(ctx, statement)
				integrationOK(t, "create temporary table", err)
			}
			if engine == dialect.Postgres {
				for i, action := range []string{"created", "updated", "deleted"} {
					_, err = conn.ExecContext(ctx, `INSERT INTO product_events (id, created_at, created_by, created_by_kind, product_id, action) VALUES ($1, NOW(), 'test', 'system', $2, $3)`, -int64(i+1), -int64(i+1), action)
					integrationOK(t, "insert allowed event action", err)
				}
				_, err = conn.ExecContext(ctx, `INSERT INTO product_events (id, created_at, created_by, created_by_kind, product_id, action) VALUES (-10, NOW(), 'test', 'system', -10, 'archived')`)
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "product_events_action_enum" {
					t.Fatalf("invalid action must fail the PostgreSQL enum constraint")
				}
				_, err = conn.ExecContext(ctx, "DELETE FROM product_events WHERE id < 0")
				integrationOK(t, "clear event enum fixture", err)
			}
			_, err = conn.ExecContext(ctx, "CREATE UNIQUE INDEX frame_event_action ON product_events (action)")
			integrationOK(t, "inject fault constraint", err)
			ids, err := id.NewSnowflake(0)
			integrationOK(t, "IDs", err)
			drv := &pinnedDriver{Driver: entsql.NewDriver(engine, entsql.Conn{ExecQuerier: conn}), conn: conn}
			client := ent.NewClient(ent.Driver(drv))
			integrationOK(t, "mutation hooks", installMutationHooks(client, entdb.MutationOptions{IDs: ids, Actor: exampleActor}))
			s, err := NewService(client)
			integrationOK(t, "service", err)
			note := "initial"
			p, err := s.Create(ctx, CreateProduct{Name: "live-test", Price: 100, Active: true, Note: &note})
			integrationOK(t, "create", err)
			found, err := s.Find(ctx, p.ID)
			integrationOK(t, "find", err)
			require.True(t, found.CreatedAt.Equal(p.CreatedAt))
			require.Zero(t, found.CreatedAt.Nanosecond()%int(time.Millisecond))
			require.Equal(t, systemActor, found.CreatedBy)
			require.Equal(t, "system", found.CreatedByKind)
			_, err = s.Create(ctx, CreateProduct{Name: "must-rollback"})
			require.Error(t, err)
			page, err := s.List(ctx, ListProducts{Input: pagination.Input{Current: 1, Size: 20}})
			integrationOK(t, "list", err)
			require.Equal(t, int64(1), page.Total)
			_, err = s.Patch(ctx, p.ID, PatchProduct{Price: optional.Field[int64]{Present: true}, Active: optional.Field[bool]{Present: true}, Note: optional.Field[string]{Present: true, Null: true}})
			integrationOK(t, "patch zero/null", err)
			_, err = s.Patch(ctx, p.ID, PatchProduct{Name: optional.Field[string]{Present: true, Value: "must-rollback"}})
			require.Error(t, err)
			found, err = s.Find(ctx, p.ID)
			integrationOK(t, "read rollback", err)
			require.Equal(t, "live-test", found.Name)
			require.Zero(t, found.Price)
			require.False(t, found.Active)
			require.Nil(t, found.Note)
			integrationOK(t, "inject delete fault", writeEvent(ctx, client, p.ID, productevent.ActionDeleted))
			require.Error(t, s.Delete(ctx, p.ID))
			_, err = s.Find(ctx, p.ID)
			integrationOK(t, "delete rollback", err)
			_, err = client.ProductEvent.Delete().Where(productevent.ActionEQ(productevent.ActionDeleted)).Exec(ctx)
			integrationOK(t, "clear delete fault", err)
			integrationOK(t, "delete", s.Delete(ctx, p.ID))
			_, err = s.Find(ctx, p.ID)
			require.ErrorIs(t, err, ErrNotFound)
			page, err = s.List(ctx, ListProducts{Input: pagination.Input{Current: 1, Size: 20}})
			integrationOK(t, "empty page", err)
			require.Empty(t, page.Records)
			require.Zero(t, page.Total)
			events, err := client.ProductEvent.Query().Count(ctx)
			integrationOK(t, "events", err)
			require.Equal(t, 3, events)
			canceled, stop := context.WithCancel(ctx)
			stop()
			_, err = s.Find(canceled, p.ID)
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}

// Production uses entdb.New; this test adapter pins temporary fixtures and every
// Ent transaction to one physical session. A replacement connection cannot fall
// through to a persistent table with the same name.
type pinnedDriver struct {
	*entsql.Driver
	conn *sql.Conn
}

func (d *pinnedDriver) Close() error                               { return nil }
func (d *pinnedDriver) Tx(ctx context.Context) (dialect.Tx, error) { return d.BeginTx(ctx, nil) }
func (d *pinnedDriver) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	tx, err := d.conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &entsql.Tx{Conn: entsql.Conn{ExecQuerier: tx}, Tx: tx}, nil
}
