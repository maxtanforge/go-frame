package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/examples/ent/ent"
	"github.com/maxtanforge/go-frame/examples/ent/ent/product"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

func TestListFiltersAndWhitelistedOrder(t *testing.T) {
	s, m := testService(t)
	m.ExpectQuery(exactSQL("SELECT COUNT(`products`.`id`) FROM `products` WHERE NOT `products`.`active`")).WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	m.ExpectQuery("SELECT .* FROM `products` WHERE NOT `products`.`active` ORDER BY `products`.`price` DESC, `products`.`id` LIMIT 2$").WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"id", "price", "active"}).AddRow(2, 90, false).AddRow(3, 90, false))
	active := false
	page, err := s.List(context.Background(), ListProducts{Input: pagination.Input{Current: 1, Size: 2}, Active: &active, Sort: "price", Desc: true})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	require.Len(t, page.Records, 2)
	require.False(t, page.Records[0].Active)
}
func TestListRejectsUnknownSortBeforeSQL(t *testing.T) {
	s, _ := testService(t)
	_, err := s.List(context.Background(), ListProducts{Input: pagination.Input{Current: 1, Size: 20}, Sort: "password"})
	require.ErrorIs(t, err, ErrInvalid)
	r := chi.NewRouter()
	require.NoError(t, Register(r, s))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/products?sort=password", nil))
	require.Equal(t, 400, w.Code)
}

func TestListRejectsExplicitEmptySortBeforeSQL(t *testing.T) {
	for _, path := range []string{"/products?sort=", "/products?sort"} {
		t.Run(path, func(t *testing.T) {
			s, _ := testService(t)
			r := chi.NewRouter()
			require.NoError(t, Register(r, s))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			require.Equal(t, 400, w.Code)
			require.Contains(t, w.Body.String(), "排序字段不能为空")
		})
	}
}
func TestGeneratedQuerySliceKeepsOriginalBuilder(t *testing.T) {
	s, m := testService(t)
	query := s.DB.Product.Query().Where(product.ActiveEQ(true)).Order(ent.Asc(product.FieldID))
	w, err := pagination.NewWindow(1, 2, pagination.DefaultLimits())
	require.NoError(t, err)
	m.ExpectQuery("SELECT .* FROM `products` WHERE `products`.`active` ORDER BY `products`.`id` ASC LIMIT 3$").WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3))
	result, err := entdb.QuerySlice(context.Background(), query, w)
	require.NoError(t, err)
	require.True(t, result.HasMore)
	require.Len(t, result.Records, 2)
	m.ExpectQuery("SELECT .* FROM `products` WHERE `products`.`active` ORDER BY `products`.`id` ASC$").WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = query.All(context.Background())
	require.NoError(t, err)
}
func TestServiceConstructionAndWrappedNotFound(t *testing.T) {
	_, err := NewService(nil)
	require.Error(t, err)
	original := &ent.NotFoundError{}
	mapped := mapNotFound(original)
	require.ErrorIs(t, mapped, ErrNotFound)
	require.ErrorIs(t, mapped, original)
	ctxErr := errors.Join(context.DeadlineExceeded, original)
	require.ErrorIs(t, mapNotFound(ctxErr), context.DeadlineExceeded)
}
func TestUnknownDatabaseConstraintStaysPrivate(t *testing.T) {
	internal := errors.New("private SQL and constraint details")
	r := chi.NewRouter()
	// Explicit application errors remain the only source of public database messages.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		respond.WriteError(w, r, httpError(errors.Join(&ent.ConstraintError{}, internal)))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 500, w.Code)
	require.False(t, strings.Contains(w.Body.String(), internal.Error()))
}

// The pinned generator drops lock modifiers on Clone. This regression documents
// why locking reads use the original transaction builder, not QueryPage/Slice.
func TestEntCloneLockBoundary(t *testing.T) {
	for _, lock := range []string{"UPDATE", "SHARE"} {
		t.Run(lock, func(t *testing.T) {
			s, m := testService(t)
			m.ExpectBegin()
			m.ExpectQuery("SELECT .* FROM `products` ORDER BY `products`.`id` ASC$").WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"id"}))
			m.ExpectQuery("SELECT .* FROM `products` ORDER BY `products`.`id` ASC FOR " + lock + "$").WithoutArgs().WillReturnRows(sqlmock.NewRows([]string{"id"}))
			m.ExpectCommit()
			err := entdb.WithTx(context.Background(), s.DB.Tx, func(tx *ent.Tx) error {
				q := tx.Product.Query().Order(ent.Asc(product.FieldID))
				if lock == "UPDATE" {
					q.ForUpdate()
				} else {
					q.ForShare()
				}
				if _, err := q.Clone().All(context.Background()); err != nil {
					return err
				}
				_, err := q.All(context.Background())
				return err
			})
			require.NoError(t, err)
		})
	}
}
