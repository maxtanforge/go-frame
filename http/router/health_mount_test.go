package router_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

type stubVerifier struct{}

func (stubVerifier) Verify(context.Context, string) (principal.Principal, error) {
	return principal.Principal{ID: "1", Issuer: "test"}, nil
}

func TestMountedHealthRoutes(t *testing.T) {
	for _, state := range []string{"ready", "failed", "draining", "missing"} {
		t.Run(state, func(t *testing.T) {
			checker, err := health.New(health.Options{Checks: []health.Check{{Name: "db", Probe: func(context.Context) error {
				if state == "failed" {
					return errors.New("offline")
				}
				return nil
			}}}})
			require.NoError(t, err)
			if state == "draining" {
				checker.SetReady(false)
			}
			if state == "missing" {
				checker = nil
			}
			outer := chi.NewRouter()
			inner := chi.NewRouter()
			inner.Mount("/service", router.New(slog.New(slog.NewTextHandler(io.Discard, nil)), router.Options{Health: checker}))
			outer.Mount("/api", inner)
			for _, method := range []string{"GET", "HEAD"} {
				for _, endpoint := range []string{"healthz", "readyz"} {
					w := httptest.NewRecorder()
					outer.ServeHTTP(w, httptest.NewRequest(method, "/api/service/"+endpoint, nil))
					want := 200
					if state == "missing" || (endpoint == "readyz" && state != "ready") {
						want = 503
					}
					require.Equal(t, want, w.Code)
				}
			}
		})
	}
}

func TestDisableHealthMountOptsOut(t *testing.T) {
	r := router.New(slog.New(slog.NewTextHandler(io.Discard, nil)), router.Options{DisableHealthMount: true})
	for _, endpoint := range []string{"/healthz", "/readyz"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, endpoint, nil))
		require.Equal(t, http.StatusNotFound, w.Code, endpoint)
	}
}

func TestDisableHealthMountExcludesCatalog(t *testing.T) {
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	authed, err := router.NewAuthenticated(lg, router.AuthenticatedOptions{
		Router:   router.Options{DisableHealthMount: true},
		Verifier: stubVerifier{},
		Routes: []router.Route{{
			Method: http.MethodGet, Path: "/things", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
		}},
	})
	require.NoError(t, err)
	for _, policy := range authed.Policies() {
		require.NotContains(t, []string{"/healthz", "/readyz"}, policy.Path)
	}
	_, err = router.NewAuthenticated(lg, router.AuthenticatedOptions{
		Router:          router.Options{DisableHealthMount: true},
		Verifier:        stubVerifier{},
		PublicEndpoints: []auth.Endpoint{{Method: http.MethodGet, Path: "/healthz"}},
	})
	require.Error(t, err, "禁用挂载后内置健康端点不得再作为 public 端点登记")
}
