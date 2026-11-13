package sqlstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/database/audit"
	"github.com/maxtanforge/go-frame/examples/records/internal/record"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

type connector struct{ state *state }

func (c connector) Connect(context.Context) (driver.Conn, error) { return connection(c), nil }
func (c connector) Driver() driver.Driver                        { return fakeDriver(c) }

type fakeDriver struct{ state *state }

func (d fakeDriver) Open(string) (driver.Conn, error) { return connection(d), nil }

type state struct {
	listRows          driver.Rows
	queryContext      context.Context
	queries           []string
	args              [][]driver.NamedValue
	committed, rolled bool
	failAudit         bool
	commitErr         error
	found             bool
}
type connection struct{ state *state }

func (c connection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c connection) Close() error              { return nil }
func (c connection) Begin() (driver.Tx, error) { return transaction(c), nil }
func (c connection) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.state.queries = append(c.state.queries, q)
	c.state.args = append(c.state.args, args)
	if c.state.failAudit && strings.Contains(q, "record_audit") {
		return nil, errors.New("audit failed")
	}
	return driver.RowsAffected(1), nil
}
func (c connection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.state.queryContext = ctx
	c.state.queries = append(c.state.queries, q)
	c.state.args = append(c.state.args, args)
	if c.state.listRows != nil {
		return c.state.listRows, nil
	}
	return &rows{found: c.state.found}, nil
}

type transaction struct{ state *state }

func (t transaction) Commit() error   { t.state.committed = true; return t.state.commitErr }
func (t transaction) Rollback() error { t.state.rolled = true; return nil }

type rows struct{ found bool }

func (*rows) Columns() []string { return strings.Split(recordColumns, ", ") }
func (*rows) Close() error      { return nil }
func (r *rows) Next(v []driver.Value) error {
	if !r.found {
		return io.EOF
	}
	r.found = false
	v[0] = int64(9007199254740993)
	v[1] = "hello"
	fillAuditValues(v)
	return nil
}

func TestSQLTransactionsAndLookup(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, fail := range []bool{false, true} {
				state := &state{failAudit: fail}
				db, err := database.Open(connector{state}, database.Options{})
				require.NoError(t, err)
				defer func() { _ = db.Close() }()
				store, err := New(db, dialect)
				require.NoError(t, err)
				svc := record.Service{IDs: testIDGenerator{}, Repository: store, Transactions: store}
				got, err := svc.Create(context.Background(), "hello", audit.Actor{Kind: audit.System, ID: "test"})
				if fail {
					require.Error(t, err)
					require.True(t, state.rolled)
					require.False(t, state.committed)
				} else {
					require.NoError(t, err)
					require.True(t, state.committed)
					require.False(t, state.rolled)
					require.Equal(t, got.ID, state.args[0][0].Value)
					require.Equal(t, got.ID, state.args[1][0].Value)
				}
				require.Len(t, state.queries, 2)
				placeholder := "?"
				if dialect == "postgres" {
					placeholder = "$1"
				}
				require.Contains(t, state.queries[0], placeholder)
				require.Contains(t, state.queries[1], placeholder)
				require.Equal(t, "hello", state.args[0][1].Value)
				require.Len(t, state.args[0], 8)
				require.Len(t, state.args[1], 5)
				require.Equal(t, state.args[0][2].Value, state.args[0][3].Value)
				require.Equal(t, state.args[0][2].Value, state.args[1][4].Value)
				require.Equal(t, "system", state.args[0][4].Value)
				require.Equal(t, "test", state.args[0][5].Value)
				require.Equal(t, state.args[0][4].Value, state.args[1][2].Value)
				require.Equal(t, state.args[0][5].Value, state.args[1][3].Value)
				require.Equal(t, state.args[0][4].Value, state.args[0][6].Value)
				require.Equal(t, state.args[0][5].Value, state.args[0][7].Value)
				_, err = store.Find(context.Background(), 9007199254740993)
				require.ErrorIs(t, err, record.ErrNotFound)
				state.found = true
				found, err := store.Find(context.Background(), 9007199254740993)
				require.NoError(t, err)
				require.Equal(t, "hello", found.Title)
				require.NoError(t, found.Validate())
				require.Equal(t, audit.Actor{Kind: audit.Subject, ID: "reader"}, found.CreatedBy)
				require.Equal(t, audit.Actor{Kind: audit.System, ID: "updater"}, found.UpdatedBy)
				require.Contains(t, state.queries[3], placeholder)
			}
		})
	}
}

func TestNewRejectsUnsupportedDialect(t *testing.T) {
	db, err := database.Open(connector{&state{}}, database.Options{})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = New(db, "sqlite")
	require.Error(t, err)
	_, err = New(nil, "postgres")
	require.Error(t, err)
}

const recordColumns = "id, title, created_at, updated_at, created_by_kind, created_by_id, updated_by_kind, updated_by_id"

func fillAuditValues(v []driver.Value) {
	v[2] = time.Date(2026, 9, 28, 1, 2, 3, 123000000, time.FixedZone("db", 3600))
	v[3] = v[2].(time.Time).Add(time.Hour)
	v[4], v[5], v[6], v[7] = "subject", "reader", "system", "updater"
}

func TestCommitFailureReturnsNoRecord(t *testing.T) {
	failure := errors.New("commit unavailable")
	st := &state{commitErr: failure}
	db, err := database.Open(connector{st}, database.Options{})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store, err := New(db, "mysql")
	require.NoError(t, err)
	got, err := (record.Service{IDs: testIDGenerator{}, Transactions: store}).Create(context.Background(), "hello", audit.Actor{Kind: audit.System, ID: "test"})
	require.ErrorIs(t, err, failure)
	require.Equal(t, record.Record{}, got)
}
func TestReadsRejectCorruptAuditFields(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		for _, bad := range []string{"actor", "precision", "chronology"} {
			for _, list := range []bool{false, true} {
				st := &state{listRows: &listRows{remaining: 1, badAudit: bad}}
				db, err := database.Open(connector{st}, database.Options{})
				require.NoError(t, err)
				store, err := New(db, dialect)
				require.NoError(t, err)
				if list {
					w, _ := pagination.NewWindow(1, 2, pagination.DefaultLimits())
					got, err := store.List(context.Background(), w, nil)
					require.ErrorIs(t, err, audit.ErrInvalid)
					require.Empty(t, got)
				} else {
					got, err := store.Find(context.Background(), 9007199254740993)
					require.ErrorIs(t, err, audit.ErrInvalid)
					require.Equal(t, record.Record{}, got)
				}
				require.NoError(t, db.Close())
			}
		}
	}
}

type testIDGenerator struct{}

func (testIDGenerator) NextContext(context.Context) (int64, error) { return 9007199254740993, nil }

func TestReadsRejectInvalidStoredID(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		for _, list := range []bool{false, true} {
			st := &state{listRows: &listRows{remaining: 1, badAudit: "id"}}
			db, err := database.Open(connector{st}, database.Options{})
			require.NoError(t, err)
			store, err := New(db, dialect)
			require.NoError(t, err)
			if list {
				w, _ := pagination.NewWindow(1, 2, pagination.DefaultLimits())
				rows, err := store.List(context.Background(), w, nil)
				require.Error(t, err)
				require.Empty(t, rows)
			} else {
				row, err := store.Find(context.Background(), 1)
				require.Error(t, err)
				require.Equal(t, record.Record{}, row)
			}
			require.NoError(t, db.Close())
		}
	}
}
