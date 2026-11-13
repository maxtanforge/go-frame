package store

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
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	generated "github.com/maxtanforge/go-frame/examples/business/internal/ent"
)

// pinnedDriver retains native Ent SQL execution but begins transactions on the
// borrowed connection. Ent's standard Driver.BeginTx requires a *sql.DB.
type pinnedDriver struct {
	*entsql.Driver
	conn *sql.Conn
}

func (d *pinnedDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	return d.BeginTx(ctx, nil)
}
func (d *pinnedDriver) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	tx, err := d.conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &entsql.Tx{Conn: entsql.Conn{ExecQuerier: tx}, Tx: tx}, nil
}

// The test owns connection and pool cleanup explicitly.
func (*pinnedDriver) Close() error { return nil }

// This test requires a dedicated database and applies the committed migration
// as session-local temporary tables. Closing the session removes every fixture.
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("FRAME_BUSINESS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set FRAME_BUSINESS_TEST_POSTGRES_DSN to a dedicated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := conn.ExecContext(ctx, "SET search_path TO pg_temp"); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../migrations/postgres/202610100001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	temporarySQL := strings.ReplaceAll(string(migration), "CREATE TABLE", "CREATE TEMP TABLE")
	for _, statement := range strings.Split(temporarySQL, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	driver := &pinnedDriver{Driver: entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: conn}), conn: conn}
	client := generated.NewClient(generated.Driver(driver))
	parentID := time.Now().UnixNano()
	id := parentID + 1
	if _, err := client.Book.Create().SetID(parentID).SetOwner("integration-owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	authorize := func(owner string) error {
		if owner != "integration-owner" {
			return category.ErrForbidden
		}
		return nil
	}
	original := category.Model{ID: ident(id), ParentID: ident(parentID), Owner: "integration-owner", Name: "created", Note: "", HasNote: true}
	created, err := s.Create(ctx, original, authorize)
	if err != nil || !created.HasNote {
		t.Fatalf("%+v %v", created, err)
	}
	changed, err := s.Update(ctx, ident(id), func(m *category.Model) error { m.Name = "changed"; m.HasNote = false; return nil })
	if err != nil || changed.HasNote {
		t.Fatalf("%+v %v", changed, err)
	}
	rejected := errors.New("reject change")
	if _, err := s.Update(ctx, ident(id), func(m *category.Model) error { m.Name = "discarded"; return rejected }); !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	found, err := s.Find(ctx, ident(id))
	if err != nil || found.Name != "changed" {
		t.Fatalf("rollback: %+v %v", found, err)
	}
	records, total, err := s.List(ctx, ident(parentID), 0, 10, authorize)
	if err != nil || total != 1 || len(records) != 1 {
		t.Fatalf("%+v %d %v", records, total, err)
	}
	if err := s.Delete(ctx, ident(id), func(category.Model) error { return category.ErrForbidden }); !errors.Is(err, category.ErrForbidden) {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, ident(id), func(category.Model) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Find(ctx, ident(id)); !errors.Is(err, category.ErrNotFound) {
		t.Fatal(err)
	}
}
