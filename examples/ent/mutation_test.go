package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// Exercise generated builders, including their required-field validation and
// generic mutation APIs, so fake mutation behavior cannot hide a hook mismatch.
func TestGeneratedCreateBulkOverwritesSystemFields(t *testing.T) {
	s, m := testService(t)
	forged := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	builder := s.DB.Product.Create().SetID(999).SetName("tea").SetPrice(0).SetActive(false).
		SetCreatedAt(forged).SetUpdatedAt(forged).SetCreatedBy("forged").SetUpdatedBy("forged").
		SetCreatedByKind("user").SetUpdatedByKind("user").SetCreatedByName("forged").SetUpdatedByName("forged")
	m.ExpectExec(exactSQL("INSERT INTO `products` (`active`, `created_at`, `created_by`, `created_by_kind`, `created_by_name`, `id`, `name`, `price`, `updated_at`, `updated_by`, `updated_by_kind`, `updated_by_name`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")).
		WithArgs(false, testNow, systemActor, "system", "", int64(123), "tea", int64(0), testNow, systemActor, "system", "").WillReturnResult(sqlmock.NewResult(0, 1))
	records, err := s.DB.Product.CreateBulk(builder).Save(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, int64(123), records[0].ID)
	require.Equal(t, testNow, records[0].CreatedAt)
	require.Equal(t, testNow, records[0].UpdatedAt)
	require.Equal(t, systemActor, records[0].CreatedBy)
	require.Equal(t, systemActor, records[0].UpdatedBy)
	require.Equal(t, "system", records[0].CreatedByKind)
	require.Equal(t, "system", records[0].UpdatedByKind)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeneratedCreateBulkSuppliesRequiredSystemFields(t *testing.T) {
	s, m := testService(t)
	m.ExpectExec(exactSQL("INSERT INTO `products` (`active`, `created_at`, `created_by`, `created_by_kind`, `created_by_name`, `id`, `name`, `price`, `updated_at`, `updated_by`, `updated_by_kind`, `updated_by_name`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")).
		WithArgs(false, testNow, systemActor, "system", "", int64(123), "tea", int64(0), testNow, systemActor, "system", "").WillReturnResult(sqlmock.NewResult(0, 1))
	records, err := s.DB.Product.CreateBulk(s.DB.Product.Create().SetName("tea").SetPrice(0).SetActive(false)).Save(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, int64(123), records[0].ID)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeneratedUpdateDiscardsForgedCreationAndPreservesSelector(t *testing.T) {
	s, m := testService(t)
	original := testNow.Add(-time.Hour)
	forged := testNow.Add(time.Hour)
	builder := s.DB.Product.UpdateOneID(456).SetName("coffee").SetUpdatedAt(forged).SetUpdatedBy("forged").SetUpdatedByKind("user")
	mutation := builder.Mutation()
	mutation.SetCreatedAt(forged)
	mutation.SetCreatedBy("forged")
	mutation.SetCreatedByKind("user")
	mutation.SetCreatedByName("forged")
	m.ExpectBegin()
	m.ExpectExec(exactSQL("UPDATE `products` SET `updated_at` = ?, `updated_by` = ?, `updated_by_kind` = ?, `updated_by_name` = ?, `name` = ? WHERE `id` = ?")).
		WithArgs(testNow, systemActor, "system", "", "coffee", int64(456)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT .* FROM `products` WHERE `id` = \\?$").WithArgs(int64(456)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "created_at", "created_by", "created_by_kind", "updated_at", "updated_by", "updated_by_kind"}).AddRow(456, "coffee", original, "original", "user", testNow, systemActor, "system"))
	m.ExpectCommit()
	record, err := builder.Save(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(456), record.ID)
	require.Equal(t, original, record.CreatedAt)
	require.Equal(t, "original", record.CreatedBy)
	require.Equal(t, "user", record.CreatedByKind)
	for _, field := range []string{"created_at", "created_by", "created_by_kind", "created_by_name"} {
		_, set := mutation.Field(field)
		require.False(t, set, field)
	}
	require.Equal(t, testNow, record.UpdatedAt)
	require.Equal(t, systemActor, record.UpdatedBy)
	require.Equal(t, "system", record.UpdatedByKind)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestHTTPRejectsEverySystemField(t *testing.T) {
	for _, method := range []string{"POST", "PATCH"} {
		for _, field := range []string{"id", "created_at", "updated_at", "created_by", "updated_by", "created_by_kind", "updated_by_kind", "created_by_name", "updated_by_name"} {
			t.Run(method+"/"+field, func(t *testing.T) {
				s, m := testService(t)
				r := chi.NewRouter()
				require.NoError(t, Register(r, s))
				path := "/products"
				if method == "PATCH" {
					path += "/123"
				}
				body, err := json.Marshal(map[string]any{"name": "tea", field: nil})
				require.NoError(t, err)
				req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				r.ServeHTTP(response, req)
				require.Equal(t, 400, response.Code)
				require.NoError(t, m.ExpectationsWereMet())
			})
		}
	}
}

func TestHTTPFindNotFound(t *testing.T) {
	s, m := testService(t)
	m.ExpectQuery("SELECT .* FROM `products` WHERE `products`.`id` = \\? LIMIT 2$").WithArgs(int64(123)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	r := chi.NewRouter()
	require.NoError(t, Register(r, s))
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("GET", "/products/123", nil))
	require.Equal(t, 404, response.Code)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGeneratedEventUsesCreationMetadataOnly(t *testing.T) {
	s, m := testService(t)
	m.ExpectExec(exactSQL("INSERT INTO "+"`product_events`"+" ("+"`action`, `created_at`, `created_by`, `created_by_kind`, `created_by_name`, `id`, `product_id`"+") VALUES (?, ?, ?, ?, ?, ?, ?)")).
		WithArgs("created", testNow, systemActor, "system", "", int64(123), int64(456)).WillReturnResult(sqlmock.NewResult(0, 1))
	events, err := s.DB.ProductEvent.CreateBulk(s.DB.ProductEvent.Create().SetProductID(456).SetAction("created").
		SetID(999).SetCreatedAt(time.Time{}).SetCreatedBy("forged").SetCreatedByKind("forged")).Save(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, int64(123), events[0].ID)
	require.Equal(t, testNow, events[0].CreatedAt)
	require.Equal(t, systemActor, events[0].CreatedBy)
	require.NoError(t, m.ExpectationsWereMet())
}
