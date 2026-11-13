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
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

type listRows struct {
	remaining, reads int
	closed           bool
	err, closeErr    error
	badScan          bool
	badAudit         string
}

func (*listRows) Columns() []string { return strings.Split(recordColumns, ", ") }
func (r *listRows) Close() error    { r.closed = true; return r.closeErr }
func (r *listRows) Next(v []driver.Value) error {
	r.reads++
	if r.remaining == 0 {
		if r.err != nil {
			return r.err
		}
		return io.EOF
	}
	r.remaining--
	v[0] = int64(9007199254740993)
	v[1] = "same title"
	fillAuditValues(v)
	switch r.badAudit {
	case "id":
		v[0] = int64(-1)
	case "actor":
		v[4] = "unknown"
	case "precision":
		v[2] = v[2].(time.Time).Add(time.Nanosecond)
	case "chronology":
		v[3] = v[2].(time.Time).Add(-time.Hour)
	}
	if r.badScan {
		v[0] = nil
	}
	return nil
}

func TestListSQL(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			rr := &listRows{remaining: 3}
			st := &state{listRows: rr}
			db, err := database.Open(connector{st}, database.Options{})
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			store, err := New(db, dialect)
			require.NoError(t, err)
			w, err := pagination.NewWindow(2, 2, pagination.DefaultLimits())
			require.NoError(t, err)
			type key struct{}
			ctx := context.WithValue(context.Background(), key{}, "marker")
			got, err := store.List(ctx, w, []pagination.Sort{{Field: "title"}})
			require.NoError(t, err)
			require.Len(t, got, 3)
			for _, row := range got {
				require.NoError(t, row.Validate())
				require.Equal(t, "reader", row.CreatedBy.ID)
			}
			require.True(t, rr.closed)
			require.Len(t, st.queries, 1)
			require.Equal(t, "marker", st.queryContext.Value(key{}))
			expected := "SELECT id, title, created_at, updated_at, created_by_kind, created_by_id, updated_by_kind, updated_by_id FROM frame_records ORDER BY `title` ASC, `id` ASC LIMIT ? OFFSET ?"
			if dialect == "postgres" {
				expected = `SELECT id, title, created_at, updated_at, created_by_kind, created_by_id, updated_by_kind, updated_by_id FROM frame_records ORDER BY "title" ASC, "id" ASC LIMIT $1 OFFSET $2`
			}
			require.Equal(t, expected, st.queries[0])
			require.Equal(t, int64(3), st.args[0][0].Value)
			require.Equal(t, int64(2), st.args[0][1].Value)
		})
	}
}
func TestListSQLFailuresAndBoundedRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rows    *listRows
		wantErr bool
	}{{"iteration error", &listRows{remaining: 1, err: errors.New("read failed")}, true}, {"error after probe", &listRows{remaining: 3, err: errors.New("terminal failure")}, true}, {"scan error", &listRows{remaining: 1, badScan: true}, true}, {"close error", &listRows{closeErr: errors.New("close failed")}, true}, {"excess driver rows", &listRows{remaining: 1000}, true}, {"empty", &listRows{}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			st := &state{listRows: tc.rows}
			db, err := database.Open(connector{st}, database.Options{})
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			store, err := New(db, "mysql")
			require.NoError(t, err)
			w, _ := pagination.NewWindow(1, 2, pagination.DefaultLimits())
			got, err := store.List(context.Background(), w, nil)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.LessOrEqual(t, len(got), 3)
			}
			require.True(t, tc.rows.closed)
			require.LessOrEqual(t, tc.rows.reads, 4)
		})
	}
}
func TestListSQLRejectsInvalidAndCanceled(t *testing.T) {
	st := &state{listRows: &listRows{}}
	db, err := database.Open(connector{st}, database.Options{})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store, err := New(db, "mysql")
	require.NoError(t, err)
	_, err = store.List(context.Background(), pagination.Window{}, nil)
	require.Error(t, err)
	w, _ := pagination.NewWindow(1, 2, pagination.DefaultLimits())
	_, err = store.List(context.Background(), w, []pagination.Sort{{Field: "title; DROP"}})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.List(ctx, w, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, st.queries)
}
