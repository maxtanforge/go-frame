package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/examples/business/internal/profile"
	"github.com/stretchr/testify/require"
)

type mockConnector struct {
	driver driver.Driver
	dsn    string
}

func (c mockConnector) Connect(context.Context) (driver.Conn, error) { return c.driver.Open(c.dsn) }
func (c mockConnector) Driver() driver.Driver                        { return c.driver }
func fixture(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	dsn := fmt.Sprintf("profile-%s", t.Name())
	pool, mock, err := sqlmock.NewWithDSN(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	db, err := database.Open(mockConnector{pool.Driver(), dsn}, database.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s, err := New(db)
	require.NoError(t, err)
	return s, mock
}
func lockedRow(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT .* FROM profiles WHERE id = \\$1 FOR UPDATE").WithArgs("bob").WillReturnRows(sqlmock.NewRows([]string{"id", "display_name", "age", "note"}).AddRow("bob", "old", 9, "keep"))
}
func TestAtomicUpdateAndChangeRecord(t *testing.T) {
	for _, mode := range []string{"commit", "audit failure", "commit failure", "callback denied", "identity mutation"} {
		t.Run(mode, func(t *testing.T) {
			s, mock := fixture(t)
			boom := errors.New("database failure")
			mock.ExpectBegin()
			lockedRow(mock)
			if mode == "callback denied" || mode == "identity mutation" {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("UPDATE profiles SET").WithArgs("new", 0, nil, "bob").WillReturnResult(sqlmock.NewResult(0, 1))
				audit := mock.ExpectExec("INSERT INTO profile_changes").WithArgs("bob", "new", 0, nil)
				if mode == "audit failure" {
					audit.WillReturnError(boom)
					mock.ExpectRollback()
				} else {
					audit.WillReturnResult(sqlmock.NewResult(0, 1))
					if mode == "commit failure" {
						mock.ExpectCommit().WillReturnError(boom)
					} else {
						mock.ExpectCommit()
					}
				}
			}
			got, err := s.Update(context.Background(), "bob", func(p *profile.Profile) error {
				p.Basic = profile.Basic{DisplayName: "new"}
				p.Note = ""
				p.HasNote = false
				if mode == "callback denied" {
					return profile.ErrForbidden
				}
				if mode == "identity mutation" {
					p.ID = "alice"
				}
				return nil
			})
			switch mode {
			case "commit":
				require.NoError(t, err)
				require.Equal(t, "new", got.Basic.DisplayName)
				require.False(t, got.HasNote)
			case "callback denied":
				require.ErrorIs(t, err, profile.ErrForbidden)
			case "identity mutation":
				require.ErrorIs(t, err, profile.ErrInvalid)
			default:
				require.ErrorIs(t, err, boom)
			}
			if err != nil {
				require.Equal(t, profile.Profile{}, got)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestMissingProfileRollsBack(t *testing.T) {
	s, mock := fixture(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FOR UPDATE").WithArgs("missing").WillReturnRows(sqlmock.NewRows([]string{"id", "display_name", "age", "note"}))
	mock.ExpectRollback()
	_, err := s.Update(context.Background(), "missing", func(*profile.Profile) error { t.Fatal("must not call callback"); return nil })
	require.ErrorIs(t, err, profile.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateFailureRollsBackWithoutChangeRecord(t *testing.T) {
	s, mock := fixture(t)
	failure := errors.New("profile write failed")
	mock.ExpectBegin()
	lockedRow(mock)
	mock.ExpectExec("UPDATE profiles SET").WithArgs("new", 9, "keep", "bob").WillReturnError(failure)
	// No change-record insert or commit is permitted after the failed update.
	mock.ExpectRollback()
	got, err := s.Update(context.Background(), "bob", func(p *profile.Profile) error {
		p.Basic.DisplayName = "new"
		return nil
	})
	require.ErrorIs(t, err, failure)
	require.Equal(t, profile.Profile{}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCallbackCancellationRollsBackWithoutWrites(t *testing.T) {
	s, mock := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock.ExpectBegin()
	lockedRow(mock)
	// Cancellation after loading may not execute either write or commit.
	mock.ExpectRollback()
	got, err := s.Update(ctx, "bob", func(p *profile.Profile) error {
		p.Basic.DisplayName = "discarded"
		cancel()
		return nil
	})
	require.Equal(t, context.Canceled, err)
	require.Equal(t, profile.Profile{}, got)
	// database/sql can race WithTx to roll back a canceled transaction.
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, time.Second, time.Millisecond)
}
