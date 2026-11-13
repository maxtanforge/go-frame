package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/examples/business/internal/devicesync"
	"github.com/stretchr/testify/require"
)

type connector struct {
	driver driver.Driver
	dsn    string
}

func (c connector) Connect(context.Context) (driver.Conn, error) { return c.driver.Open(c.dsn) }
func (c connector) Driver() driver.Driver                        { return c.driver }
func fixture(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	dsn := fmt.Sprintf("device-%s", t.Name())
	pool, mock, err := sqlmock.NewWithDSN(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	db, err := database.Open(connector{pool.Driver(), dsn}, database.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s, err := New(db)
	require.NoError(t, err)
	return s, mock
}
func eventRows(delivered bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "steps", "delivered"}).AddRow("event-1", "user-1", 20, delivered)
}

func TestPutCommitsImmutableEventOrRejectsConflict(t *testing.T) {
	for _, mode := range []string{"new", "duplicate", "conflict", "commit failure"} {
		t.Run(mode, func(t *testing.T) {
			s, mock := fixture(t)
			ctx := context.Background()
			event := devicesync.Event{ID: "event-1", UserID: "user-1", Steps: 20}
			if mode == "conflict" {
				event.Steps = 21
			}
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO device_events").WithArgs(event.ID, event.UserID, event.Steps).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT id, user_id, steps, delivered FROM device_events").WithArgs(event.ID).WillReturnRows(eventRows(mode == "duplicate"))
			commitErr := errors.New("commit outcome unknown")
			switch mode {
			case "conflict":
				mock.ExpectRollback()
			case "commit failure":
				mock.ExpectCommit().WillReturnError(commitErr)
			default:
				mock.ExpectCommit()
			}
			r, err := s.Put(ctx, event)
			switch mode {
			case "conflict":
				require.ErrorIs(t, err, devicesync.ErrConflict)
				require.Equal(t, devicesync.Record{}, r)
			case "commit failure":
				require.ErrorIs(t, err, commitErr)
				require.Equal(t, devicesync.Record{}, r)
			default:
				require.NoError(t, err)
				require.Equal(t, event, r.Event)
				require.Equal(t, mode == "duplicate", r.Delivered)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestPendingIsBoundedAndPropagatesReadErrors(t *testing.T) {
	s, mock := fixture(t)
	_, err := s.Pending(context.Background(), 0)
	require.ErrorIs(t, err, devicesync.ErrInvalid)
	_, err = s.Pending(context.Background(), 101)
	require.ErrorIs(t, err, devicesync.ErrInvalid)
	mock.ExpectQuery("WHERE delivered = FALSE ORDER BY id LIMIT ").WithArgs(2).WillReturnRows(eventRows(false))
	got, err := s.Pending(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, []string{"event-1"}, got)
	readErr := errors.New("row read failed")
	mock.ExpectQuery("WHERE delivered = FALSE ORDER BY id LIMIT ").WithArgs(2).WillReturnRows(eventRows(false).RowError(0, readErr))
	_, err = s.Pending(context.Background(), 2)
	require.ErrorIs(t, err, readErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestFindAndCompleteDoNotHideMissingOrFailedWrites(t *testing.T) {
	s, mock := fixture(t)
	mock.ExpectQuery("SELECT id, user_id, steps, delivered FROM device_events").WithArgs("event-1").WillReturnRows(eventRows(false))
	r, err := s.Find(context.Background(), "event-1")
	require.NoError(t, err)
	require.False(t, r.Delivered)
	mock.ExpectQuery("SELECT id, user_id, steps, delivered FROM device_events").WithArgs("missing").WillReturnError(sql.ErrNoRows)
	_, err = s.Find(context.Background(), "missing")
	require.ErrorIs(t, err, devicesync.ErrNotFound)
	mock.ExpectExec("UPDATE device_events SET delivered = TRUE").WithArgs("event-1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Complete(context.Background(), "event-1"))
	mock.ExpectExec("UPDATE device_events SET delivered = TRUE").WithArgs("missing").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Complete(context.Background(), "missing"), devicesync.ErrNotFound)
	writeErr := errors.New("connection lost")
	mock.ExpectExec("UPDATE device_events SET delivered = TRUE").WithArgs("event-1").WillReturnError(writeErr)
	require.ErrorIs(t, s.Complete(context.Background(), "event-1"), writeErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestNewRejectsMissingPool(t *testing.T) {
	_, err := New(nil)
	require.Error(t, err)
}
