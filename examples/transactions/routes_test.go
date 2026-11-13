package transactionexample

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

func TestExplicitAndMetaRoutesExecuteSameBusinessQuery(t *testing.T) {
	for _, meta := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "meta"}[meta], func(t *testing.T) {
			svc, _ := serviceFixture(t)
			calls := 0
			list := func(_ context.Context, q ListQuery) ([]Product, error) {
				calls++
				require.Equal(t, ListQuery{Category: "tea", Limit: 7}, q)
				return []Product{{SKU: "SKU-1", Name: "Tea"}}, nil
			}
			build := Routes
			if meta {
				build = MetaRoutes
			}
			routes, err := build(svc, list)
			require.NoError(t, err)
			require.Len(t, routes, 2)
			r := chi.NewRouter()
			for _, route := range routes {
				require.False(t, route.Public)
				r.Method(route.Method, route.Path, route.Handler)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/categories/tea/products?limit=7", nil))
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), `"sku":"SKU-1"`)
			require.Equal(t, 1, calls)
		})
	}
}

func TestMetaAndExplicitPolicyDeclarationsMatch(t *testing.T) {
	list := func(context.Context, ListQuery) ([]Product, error) { return nil, nil }
	explicit, err := Routes(Service{}, list)
	require.NoError(t, err)
	scanned, err := MetaRoutes(Service{}, list)
	require.NoError(t, err)
	for i := range explicit {
		require.Equal(t, explicit[i].Method, scanned[i].Method)
		require.Equal(t, explicit[i].Path, scanned[i].Path)
		require.Equal(t, explicit[i].Public, scanned[i].Public)
		require.Equal(t, explicit[i].Operation, scanned[i].Operation)
	}
}

func TestMetaScanRetainsFactoryCauseAndRejectsUnsupportedFields(t *testing.T) {
	boom := errors.New("invalid DTO schema")
	factory := func() (http.Handler, error) { return nil, boom }
	_, err := scanRoutes("products", struct {
		Create func() (http.Handler, error) `method:"POST" path:"/products"`
	}{factory})
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), "POST")
	require.Contains(t, err.Error(), "/products")
	for _, controller := range []any{
		struct {
			Value int `method:"GET" path:"/products"`
		}{1},
		struct {
			Get func() (http.Handler, error) `method:"GET" path:"/products" public:"yes"`
		}{factory},
		struct {
			Get func() (http.Handler, error) `method:"GET" path:"/products"`
		}{},
		struct {
			Get func() (http.Handler, error) `method:"GET"`
		}{factory},
	} {
		_, err = scanRoutes("products", controller)
		require.Error(t, err)
	}
	_, err = scanRoutes("products", nil)
	require.Error(t, err)
}

func TestRoutesStopStartupForMissingUseCase(t *testing.T) {
	for _, build := range []func(Service, func(context.Context, ListQuery) ([]Product, error)) ([]router.Route, error){Routes, MetaRoutes} {
		routes, err := build(Service{}, nil)
		require.Error(t, err)
		require.Nil(t, routes)
		require.True(t, strings.Contains(err.Error(), "products") && strings.Contains(err.Error(), "GET"))
	}
}
