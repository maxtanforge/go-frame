package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/optional"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func exactSQL(s string) string { return "^" + regexp.QuoteMeta(s) + "$" }

type fixedIDs struct{}

func (fixedIDs) NextContext(context.Context) (int64, error) { return 123, nil }
func testService(t *testing.T) (Service, sqlmock.Sqlmock) {
	t.Helper()
	pool, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, pool.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	db, err := newClient(pool, "mysql", entdb.Options{}, entdb.MutationOptions{IDs: fixedIDs{}, Actor: exampleActor, Now: func() time.Time { return testNow }})
	require.NoError(t, err)
	service, err := NewService(db)
	require.NoError(t, err)
	return service, mock
}
func TestCreateAtomic(t *testing.T) {
	eventErr := errors.New("event unavailable")
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			s, m := testService(t)
			m.ExpectBegin()
			m.ExpectExec(exactSQL("INSERT INTO `products` (`created_at`, `updated_at`, `created_by`, `updated_by`, `created_by_kind`, `updated_by_kind`, `created_by_name`, `updated_by_name`, `name`, `price`, `active`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")).WithArgs(testNow, testNow, systemActor, systemActor, "system", "system", "", "", "tea", int64(0), false, int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
			event := m.ExpectExec(exactSQL("INSERT INTO `product_events` (`created_at`, `created_by`, `created_by_kind`, `created_by_name`, `product_id`, `action`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?)")).WithArgs(testNow, systemActor, "system", "", int64(123), "created", int64(123))
			if fail {
				event.WillReturnError(eventErr)
				m.ExpectRollback()
			} else {
				event.WillReturnResult(sqlmock.NewResult(1, 1))
				m.ExpectCommit()
			}
			p, err := s.Create(context.Background(), CreateProduct{Name: "tea"})
			if fail {
				require.ErrorIs(t, err, eventErr)
				require.Nil(t, p)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(123), p.ID)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestPatchZeroValues(t *testing.T) {
	s, m := testService(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM `products` WHERE `products`.`id` = \\? LIMIT 2 FOR UPDATE$").WithArgs(int64(123)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price", "active", "note"}).AddRow(123, "tea", 90, true, "old"))
	m.ExpectExec(exactSQL("UPDATE `products` SET `note` = NULL, `updated_at` = ?, `updated_by` = ?, `updated_by_kind` = ?, `updated_by_name` = ?, `price` = ?, `active` = ? WHERE `id` = ?")).WithArgs(testNow, systemActor, "system", "", int64(0), false, int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* FROM `products` WHERE `id` = \\?$").WithArgs(int64(123)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price", "active", "note"}).AddRow(123, "tea", 0, false, nil))
	m.ExpectExec(exactSQL("INSERT INTO `product_events` (`created_at`, `created_by`, `created_by_kind`, `created_by_name`, `product_id`, `action`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?)")).WithArgs(testNow, systemActor, "system", "", int64(123), "updated", int64(123)).WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	p, err := s.Patch(context.Background(), 123, PatchProduct{Price: optional.Field[int64]{Present: true}, Active: optional.Field[bool]{Present: true}, Note: optional.Field[string]{Present: true, Null: true}})
	require.NoError(t, err)
	require.Zero(t, p.Price)
	require.False(t, p.Active)
	require.Nil(t, p.Note)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestInvalidHTTPDoesNotQuery(t *testing.T) {
	for _, body := range []string{`{"name":""}`, `{"name":"tea","id":"9"}`, `{"name":"tea","created_at":0}`, `{"price":null}`, `{"active":null}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			s, m := testService(t)
			r := chi.NewRouter()
			require.NoError(t, Register(r, s))
			method, path := "PATCH", "/products/123"
			if strings.Contains(body, "name") {
				method, path = "POST", "/products"
			}
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 400, w.Code)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestListPage(t *testing.T) {
	s, m := testService(t)
	m.ExpectQuery(exactSQL("SELECT COUNT(`products`.`id`) FROM `products`")).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	m.ExpectQuery("SELECT .* FROM `products` ORDER BY `products`.`id` LIMIT 2 OFFSET 2$").WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(3, "tea"))
	p, err := s.List(context.Background(), ListProducts{Input: pagination.Input{Current: 2, Size: 2}})
	require.NoError(t, err)
	require.Equal(t, int64(2), p.Pages)
	require.Equal(t, int64(3), p.Total)
	require.Len(t, p.Records, 1)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestDeleteAtomic(t *testing.T) {
	eventErr := errors.New("event unavailable")
	s, m := testService(t)
	m.ExpectBegin()
	m.ExpectExec(exactSQL("DELETE FROM `products` WHERE `products`.`id` = ?")).WithArgs(int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec(exactSQL("INSERT INTO `product_events` (`created_at`, `created_by`, `created_by_kind`, `created_by_name`, `product_id`, `action`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?)")).WithArgs(testNow, systemActor, "system", "", int64(123), "deleted", int64(123)).WillReturnError(eventErr)
	m.ExpectRollback()
	require.ErrorIs(t, s.Delete(context.Background(), 123), eventErr)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestFindNotFound(t *testing.T) {
	s, m := testService(t)
	m.ExpectQuery("SELECT .* FROM `products` WHERE `products`.`id` = \\? LIMIT 2$").WithArgs(int64(123)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err := s.Find(context.Background(), 123)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestNoteNULRejectedBeforeSQL(t *testing.T) {
	s, m := testService(t)
	note := "bad\x00note"
	_, err := s.Create(context.Background(), CreateProduct{Name: "tea", Note: &note})
	require.ErrorIs(t, err, validation.ErrInvalid)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestPatchEventFailureRollsBack(t *testing.T) {
	eventErr := errors.New("event unavailable")
	s, m := testService(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM `products` WHERE `products`.`id` = \\? LIMIT 2 FOR UPDATE$").WithArgs(int64(123)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price", "active"}).AddRow(123, "tea", 0, false))
	// An unchanged MySQL update may return zero affected rows and must still write its event.
	m.ExpectExec(exactSQL("UPDATE `products` SET `updated_at` = ?, `updated_by` = ?, `updated_by_kind` = ?, `updated_by_name` = ?, `active` = ? WHERE `id` = ?")).WithArgs(testNow, systemActor, "system", "", false, int64(123)).WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectQuery("SELECT .* FROM `products` WHERE `id` = \\?$").WithArgs(int64(123)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price", "active"}).AddRow(123, "tea", 0, false))
	m.ExpectExec(exactSQL("INSERT INTO `product_events` (`created_at`, `created_by`, `created_by_kind`, `created_by_name`, `product_id`, `action`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?)")).WithArgs(testNow, systemActor, "system", "", int64(123), "updated", int64(123)).WillReturnError(eventErr)
	m.ExpectRollback()
	record, err := s.Patch(context.Background(), 123, PatchProduct{Active: optional.Field[bool]{Present: true}})
	require.ErrorIs(t, err, eventErr)
	require.Nil(t, record)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestStrictQueryAndPath(t *testing.T) {
	for _, path := range []string{"/products?size=0", "/products?unknown=id", "/products?size=1&size=2", "/products/no", "/products/0"} {
		t.Run(path, func(t *testing.T) {
			s, m := testService(t)
			r := chi.NewRouter()
			require.NoError(t, Register(r, s))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			require.Equal(t, 400, w.Code)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestPatchEmptyNotePreservesAbsentFields(t *testing.T) {
	s, m := testService(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT .* FROM `products` WHERE `products`.`id` = \\? LIMIT 2 FOR UPDATE$").WithArgs(int64(123)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price", "active", "note"}).AddRow(123, "tea", 90, true, "old"))
	m.ExpectExec(exactSQL("UPDATE `products` SET `updated_at` = ?, `updated_by` = ?, `updated_by_kind` = ?, `updated_by_name` = ?, `note` = ? WHERE `id` = ?")).WithArgs(testNow, systemActor, "system", "", "", int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* FROM `products` WHERE `id` = \\?$").WithArgs(int64(123)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price", "active", "note"}).AddRow(123, "tea", 90, true, ""))
	m.ExpectExec(exactSQL("INSERT INTO `product_events` (`created_at`, `created_by`, `created_by_kind`, `created_by_name`, `product_id`, `action`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?)")).WithArgs(testNow, systemActor, "system", "", int64(123), "updated", int64(123)).WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	r := chi.NewRouter()
	require.NoError(t, Register(r, s))
	req := httptest.NewRequest("PATCH", "/products/123", strings.NewReader(`{"note":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	var body struct {
		Data struct {
			Name   string  `json:"name"`
			Price  int64   `json:"price"`
			Active bool    `json:"active"`
			Note   *string `json:"note"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "tea", body.Data.Name)
	require.Equal(t, int64(90), body.Data.Price)
	require.True(t, body.Data.Active)
	require.NotNil(t, body.Data.Note)
	require.Empty(t, *body.Data.Note)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestHTTPProductIDAndMillisecondTimes(t *testing.T) {
	s, m := testService(t)
	const productID int64 = 9007199254740993
	created := time.Date(2026, 1, 2, 8, 4, 5, 123000000, time.FixedZone("UTC+8", 8*60*60))
	updated := created.Add(time.Hour)
	m.ExpectQuery("SELECT .* FROM `products` WHERE `products`.`id` = \\? LIMIT 2$").WithArgs(productID).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "created_at", "updated_at"}).AddRow(productID, "tea", created, updated))
	r := chi.NewRouter()
	require.NoError(t, Register(r, s))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/products/9007199254740993", nil))
	require.Equal(t, 200, w.Code)
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.JSONEq(t, `"9007199254740993"`, string(body.Data["id"]))
	require.JSONEq(t, strconv.FormatInt(created.UTC().UnixMilli(), 10), string(body.Data["created_at"]))
	require.JSONEq(t, strconv.FormatInt(updated.UTC().UnixMilli(), 10), string(body.Data["updated_at"]))
	require.NoError(t, m.ExpectationsWereMet())
}
func TestHTTPEmptyPageHasFiveFieldsAndArray(t *testing.T) {
	s, m := testService(t)
	m.ExpectQuery(exactSQL("SELECT COUNT(`products`.`id`) FROM `products`")).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.ExpectQuery("SELECT .* FROM `products` ORDER BY `products`.`id` LIMIT 20$").WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	r := chi.NewRouter()
	require.NoError(t, Register(r, s))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/products", nil))
	require.Equal(t, 200, w.Code)
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.JSONEq(t, `{"current":1,"size":20,"total":0,"pages":0,"records":[]}`, string(body.Data))
	require.NoError(t, m.ExpectationsWereMet())
}
