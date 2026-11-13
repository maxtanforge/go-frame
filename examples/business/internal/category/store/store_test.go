package store

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	generated "github.com/maxtanforge/go-frame/examples/business/internal/ent"
)

func ident(n int64) category.ID { id, _ := category.NewID(n); return id }
func fixture(t *testing.T, driver string) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	client := generated.NewClient(generated.Driver(entsql.OpenDB(driver, db)))
	t.Cleanup(func() {
		_ = client.Close()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	s, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	return s, mock
}
func row(system bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "parent_id", "owner", "name", "note", "system"}).AddRow(2, 1, "alice", "old", "note", system)
}
func bookRow() *sqlmock.Rows { return sqlmock.NewRows([]string{"id", "owner"}).AddRow(1, "alice") }
func TestNewRejectsNil(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil client accepted")
	}
}
func TestFindSnapshotAndMissing(t *testing.T) {
	s, m := fixture(t, dialect.Postgres)
	m.ExpectQuery(`SELECT .* FROM "business_categories" WHERE .*id.*`).WithArgs(int64(2)).WillReturnRows(row(false))
	got, err := s.Find(context.Background(), ident(2))
	if err != nil || got.Note != "note" || !got.HasNote || got.Owner != "alice" {
		t.Fatalf("%+v %v", got, err)
	}
	m.ExpectQuery(`SELECT .*business_categories.*`).WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = s.Find(context.Background(), ident(3))
	var cause *generated.NotFoundError
	if !errors.Is(err, category.ErrNotFound) || !errors.As(err, &cause) {
		t.Fatalf("missing causes: %v", err)
	}
}
func TestCreateAuthorizationAndSuccess(t *testing.T) {
	for _, forbidden := range []bool{true, false} {
		t.Run(map[bool]string{true: "forbidden", false: "success"}[forbidden], func(t *testing.T) {
			s, m := fixture(t, dialect.Postgres)
			m.ExpectBegin()
			m.ExpectQuery(`SELECT .*business_books.*FOR UPDATE`).WithArgs(int64(1)).WillReturnRows(bookRow())
			if forbidden {
				m.ExpectRollback()
			} else {
				m.ExpectExec(`INSERT INTO "business_categories" .*`).WithArgs("alice", "new", "", false, int64(1), int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit()
			}
			got, err := s.Create(context.Background(), category.Model{ID: ident(2), ParentID: ident(1), Owner: "alice", Name: "new", Note: "", HasNote: true}, func(owner string) error {
				if owner != "alice" {
					t.Fatal(owner)
				}
				if forbidden {
					return category.ErrForbidden
				}
				return nil
			})
			if forbidden {
				if !errors.Is(err, category.ErrForbidden) {
					t.Fatal(err)
				}
			} else if err != nil || got.Name != "new" || !got.HasNote || got.System {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}
func TestUpdateClearNote(t *testing.T) {
	s, m := fixture(t, dialect.Postgres)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT .*business_categories.*FOR UPDATE`).WithArgs(int64(2)).WillReturnRows(row(false))
	m.ExpectExec(`UPDATE "business_categories" SET .*"note" = NULL.*WHERE .*id.*`).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery(`SELECT .*business_categories.*`).WillReturnRows(sqlmock.NewRows([]string{"id", "parent_id", "owner", "name", "note", "system"}).AddRow(2, 1, "alice", "new", nil, false))
	m.ExpectCommit()
	got, err := s.Update(context.Background(), ident(2), func(v *category.Model) error { v.Name = "new"; v.Note = ""; v.HasNote = false; return nil })
	if err != nil || got.Name != "new" || got.HasNote {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestUpdateRejectsCallbackAndImmutableChanges(t *testing.T) {
	for _, which := range []string{"forbidden", "system", "id", "parent", "owner", "system-change", "cancel"} {
		t.Run(which, func(t *testing.T) {
			s, m := fixture(t, dialect.Postgres)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.ExpectBegin()
			m.ExpectQuery(`SELECT .*business_categories.*FOR UPDATE`).WillReturnRows(row(which == "system"))
			m.ExpectRollback()
			expected := category.ErrImmutable
			_, err := s.Update(ctx, ident(2), func(v *category.Model) error {
				switch which {
				case "forbidden":
					expected = category.ErrForbidden
					return expected
				case "system":
					return expected
				case "id":
					v.ID = ident(3)
				case "parent":
					v.ParentID = ident(3)
				case "owner":
					v.Owner = "bob"
				case "system-change":
					v.System = true
				case "cancel":
					expected = context.Canceled
					cancel()
				}
				return nil
			})
			if !errors.Is(err, expected) {
				t.Fatalf("want %v got %v", expected, err)
			}
		})
	}
}
func TestDeleteChecksBeforeWrite(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		t.Run(map[bool]string{true: "blocked", false: "success"}[blocked], func(t *testing.T) {
			s, m := fixture(t, dialect.Postgres)
			m.ExpectBegin()
			m.ExpectQuery(`SELECT .*business_categories.*FOR UPDATE`).WillReturnRows(row(blocked))
			if blocked {
				m.ExpectRollback()
			} else {
				m.ExpectExec(`DELETE FROM "business_categories" WHERE .*id.*`).WithArgs(int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit()
			}
			err := s.Delete(context.Background(), ident(2), func(v category.Model) error {
				if v.System {
					return category.ErrImmutable
				}
				return nil
			})
			if blocked {
				if !errors.Is(err, category.ErrImmutable) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestListAuthorizationPredicatesAndPaging(t *testing.T) {
	s, m := fixture(t, dialect.Postgres)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT .*business_books.*`).WithArgs(int64(1)).WillReturnRows(bookRow())
	m.ExpectQuery(`SELECT COUNT\(.*business_categories.*"parent_id" = \$1 AND .*"owner" = \$2`).WithArgs(int64(1), "alice").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.ExpectQuery(`SELECT .*business_categories.*"parent_id" = \$1 AND .*"owner" = \$2 ORDER BY .*"id" ASC LIMIT 10 OFFSET 20`).WithArgs(int64(1), "alice").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectCommit()
	got, total, err := s.List(context.Background(), ident(1), 20, 10, func(owner string) error {
		if owner != "alice" {
			t.Fatal(owner)
		}
		return nil
	})
	if err != nil || got == nil || len(got) != 0 || total != 0 {
		t.Fatalf("%+v %d %v", got, total, err)
	}
}
func TestListForbiddenAndInvalid(t *testing.T) {
	s, m := fixture(t, dialect.Postgres)
	for _, args := range [][2]int64{{-1, 1}, {0, 0}, {0, 101}, {1000001, 1}} {
		_, _, err := s.List(context.Background(), ident(1), args[0], args[1], func(string) error { return nil })
		if !errors.Is(err, category.ErrInvalid) {
			t.Fatal(err)
		}
	}
	m.ExpectBegin()
	m.ExpectQuery(`SELECT .*business_books.*`).WillReturnRows(bookRow())
	m.ExpectRollback()
	_, _, err := s.List(context.Background(), ident(1), 0, 10, func(string) error { return category.ErrForbidden })
	if !errors.Is(err, category.ErrForbidden) {
		t.Fatal(err)
	}
}
func TestBackendFailureRollsBackPreservingCause(t *testing.T) {
	s, m := fixture(t, dialect.Postgres)
	failure := errors.New("backend private")
	m.ExpectBegin()
	m.ExpectQuery(`SELECT .*business_categories.*FOR UPDATE`).WillReturnRows(row(false))
	m.ExpectExec(`DELETE FROM`).WillReturnError(failure)
	m.ExpectRollback()
	err := s.Delete(context.Background(), ident(2), func(category.Model) error { return nil })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
func TestMySQLLockSmoke(t *testing.T) {
	s, m := fixture(t, dialect.MySQL)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .*`business_categories`.*FOR UPDATE").WithArgs(int64(2)).WillReturnRows(row(false))
	m.ExpectRollback()
	err := s.Delete(context.Background(), ident(2), func(category.Model) error { return category.ErrForbidden })
	if !errors.Is(err, category.ErrForbidden) {
		t.Fatal(err)
	}
}
