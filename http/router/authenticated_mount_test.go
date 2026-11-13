package router_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedMountedPolicy(t *testing.T) {
	for _, tc := range []struct {
		mount, path string
		status      int
	}{
		{"/api", "/api/login", 204},
		{"/api", "/api/", 401},
		{"/login", "/login", 401},
		{"/login", "/login/", 401},
		{"/login", "/login/login", 204},
		{"/api", "/api/%6cogin", 401},
		{"/api", "/api/login/", 401},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{
				Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
					return principal.Principal{}, principal.ErrUnauthenticated
				}),
				Routes: []router.Route{
					{Method: "GET", Path: "/login", Public: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })},
					{Method: "GET", Path: "/", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("protected handler executed without credentials") })},
				},
			})
			require.NoError(t, err)
			parent := chi.NewRouter()
			parent.Mount(tc.mount, r)
			w := httptest.NewRecorder()
			parent.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestAuthenticatedNestedLateMountedRoutesStayProtected(t *testing.T) {
	r, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{
		Router:   router.Options{DisableHealthMount: true},
		Verifier: stubVerifier{},
		Routes:   []router.Route{{Method: "GET", Path: "/login", Public: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })}},
	})
	require.NoError(t, err)
	child := chi.NewRouter()
	child.Get("/login", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	child.Get("/items/{id}", func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "42", chi.URLParam(req, "id"))
		require.Equal(t, "/api/service/late/items/42", req.URL.Path)
		w.WriteHeader(204)
	})
	r.Mount("/late", child)
	parent := chi.NewRouter()
	inner := chi.NewRouter()
	inner.Mount("/service", r)
	parent.Mount("/api", inner)
	for _, tc := range []struct {
		path          string
		authenticated bool
		status        int
	}{
		{"/api/service/login", false, 204},
		{"/api/service/%6cogin", false, 401},
		{"/api/service/late/login", false, 401},
		{"/api/service/late/items/42", false, 401},
		{"/api/service/late/items/42", true, 204},
		{"/api/service/late/missing", false, 401},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", tc.path, nil)
			if tc.authenticated {
				req.Header.Set("Authorization", "Bearer valid")
			}
			parent.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
		})
	}
	require.Len(t, r.Policies(), 1, "late mounts must not alter the startup catalog")
}
