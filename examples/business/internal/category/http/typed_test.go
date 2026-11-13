package categoryhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/examples/contracts/endpoint"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

// TypedRoutes keeps the experimental composition comparison in the test build.
// Both paths use the production Handler callbacks and declaration metadata.
func TypedRoutes(service category.Service) ([]router.Route, error) {
	if service.Repository == nil {
		return nil, errors.New("category repository required")
	}
	h := Handler{service: service}
	return router.Collect("categories", endpoint.Path(detailRoute(), h.Detail, detailOptions()), endpoint.JSONWithPath(patchRoute(), h.Patch, detailOptions()))
}

func TestTypedCategoryContractMatchesExplicit(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, actor string
		status                          int
	}{
		{"detail", "GET", "/categories/1", "", "alice", 200},
		{"large ID", "GET", "/categories/9223372036854775807", "", "alice", 200},
		{"omitted", "PATCH", "/categories/1", `{"name":"new"}`, "alice", 200},
		{"clear", "PATCH", "/categories/1", `{"note":null}`, "alice", 200},
		{"empty", "PATCH", "/categories/1", `{"note":""}`, "alice", 200},
		{"value", "PATCH", "/categories/1", `{"note":"new"}`, "alice", 200},
		{"invalid ID", "GET", "/categories/01", "", "alice", 400},
		{"nonnullable", "PATCH", "/categories/1", `{"name":null}`, "alice", 400},
		{"private field", "PATCH", "/categories/1", `{"owner":"bob"}`, "alice", 400},
		{"protected", "GET", "/categories/1", "", "", 401},
		{"permission", "PATCH", "/categories/1", `{"note":null}`, "bob", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var baselineBody map[string]any
			var baselineModel category.Model
			var baselinePolicies []router.RoutePolicy
			for i, build := range []func(category.Service) ([]router.Route, error){Routes, TypedRoutes} {
				repo := fixture()
				if tc.name == "large ID" {
					repo.model.ID, _ = category.ParseID("9223372036854775807")
				}
				routes, err := build(category.Service{Repository: repo})
				require.NoError(t, err)
				r, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{
					Router: router.Options{DisableHealthMount: true}, Routes: routes,
					Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
						return principal.Principal{ID: tc.actor, Issuer: "test"}, nil
					}),
				})
				require.NoError(t, err)
				req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				if tc.actor != "" {
					req.Header.Set("Authorization", "Bearer test")
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, tc.status, w.Code, w.Body.String())
				var envelope map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
				require.NotEmpty(t, envelope["traceId"])
				require.NotZero(t, envelope["timestamp"])
				// Each request receives its own trace and clock; compare stable contract fields.
				delete(envelope, "traceId")
				delete(envelope, "timestamp")
				if i == 0 {
					baselineBody, baselineModel, baselinePolicies = envelope, repo.model, r.Policies()
				} else {
					require.Equal(t, baselineBody, envelope)
					require.Equal(t, baselineModel, repo.model)
					require.Equal(t, baselinePolicies, r.Policies())
				}
				require.NotContains(t, w.Body.String(), `"owner"`)
				require.NotContains(t, w.Body.String(), `"system"`)
				if tc.status == http.StatusBadRequest || tc.status == http.StatusUnauthorized {
					require.Zero(t, repo.updates)
				}
			}
		})
	}
}

func TestTypedCategoryInternalErrorStaysPrivate(t *testing.T) {
	for _, build := range []func(category.Service) ([]router.Route, error){Routes, TypedRoutes} {
		repo := fixture()
		repo.failure = errors.New("private database password")
		routes, err := build(category.Service{Repository: repo})
		require.NoError(t, err)
		req := httptest.NewRequest("GET", "/categories/1", nil)
		req = req.WithContext(principal.IntoContext(req.Context(), principal.Principal{ID: "alice", Issuer: "test"}))
		w := httptest.NewRecorder()
		r := chi.NewRouter()
		for _, route := range routes {
			r.Method(route.Method, route.Path, route.Handler)
		}
		r.ServeHTTP(w, req)
		require.Equal(t, 500, w.Code)
		require.NotContains(t, w.Body.String(), "password")
	}
}
