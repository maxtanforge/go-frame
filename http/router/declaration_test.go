package router_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/router"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

func TestCollectBindingSchemaError(t *testing.T) {
	type input struct {
		ID string `path:"id"`
	}
	handler := func(context.Context, *input) (*input, error) { t.Fatal("handler ran at startup"); return nil, nil }
	routes, err := router.Collect("orders",
		router.Route{Method: "GET", Path: "/ok"}.Bind(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), nil),
		router.Route{Method: "GET", Path: "/orders/{id}"}.Bind(binding.Path(handler)),
	)
	require.Nil(t, routes)
	require.ErrorIs(t, err, validation.ErrSchema)
	for _, part := range []string{"orders", "GET", "/orders/{id}"} {
		require.ErrorContains(t, err, part)
	}
}

func TestCollectPreservesBindingCause(t *testing.T) {
	cause := errors.New("cannot compile DTO")
	routes, err := router.Collect("inventory", router.Route{Method: "POST", Path: "/items"}.Bind(nil, cause))
	require.Nil(t, routes)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "inventory")
	require.ErrorContains(t, err, "POST")
	require.ErrorContains(t, err, "/items")
}

func TestCollectRejectsInvalidDeclarations(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	var typedNil http.HandlerFunc
	for _, tc := range []struct {
		name    string
		route   router.Route
		handler http.Handler
	}{
		{"method", router.Route{Method: "get", Path: "/bad"}, h},
		{"path", router.Route{Method: "GET", Path: "bad"}, h},
		{"metadata", router.Route{Method: "GET", Path: "/bad", Operation: operationhttp.Metadata{Action: "bad\n"}}, h},
		{"handler", router.Route{Method: "GET", Path: "/bad"}, nil},
		{"typed nil", router.Route{Method: "GET", Path: "/bad"}, typedNil},
		{"public pattern", router.Route{Method: "GET", Path: "/bad/{id}", Public: true}, h},
		{"chi pattern", router.Route{Method: "GET", Path: "/bad/{id"}, h},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routes, err := router.Collect("inventory", tc.route.Bind(tc.handler, nil))
			require.Nil(t, routes)
			require.ErrorContains(t, err, "inventory")
			require.ErrorContains(t, err, tc.route.Method)
			require.ErrorContains(t, err, tc.route.Path)
		})
	}
	routes, err := router.Collect("inventory", router.Route{Method: "GET", Path: "/same"}.Bind(h, nil), router.Route{Method: "GET", Path: "/same"}.Bind(h, nil))
	require.Nil(t, routes)
	require.ErrorContains(t, err, "duplicate")
	require.ErrorContains(t, err, "/same")
	_, err = router.Collect("", router.Route{Method: "GET", Path: "/same"}.Bind(h, nil))
	require.Error(t, err)
}

func TestCollectedRoutesPreserveAuthenticatedCatalog(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	routes, err := router.Collect("inventory",
		router.Route{Method: "POST", Path: "/login", Public: true, Operation: operationhttp.Metadata{Action: "login"}}.Bind(h, nil),
		router.Route{Method: "GET", Path: "/items/{id}"}.Bind(h, nil),
	)
	require.NoError(t, err)
	r, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{Routes: routes, Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{}, principal.ErrUnauthenticated
	})})
	require.NoError(t, err)
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"POST", "/login", 204}, {"GET", "/items/1", 401}, {"GET", "/healthz", 401}, {"GET", "/missing", 401}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, tc.status, w.Code)
	}
	policies := r.Policies()
	require.Len(t, policies, 6)
	for _, p := range policies {
		require.Equal(t, p.Path, p.Operation.Route)
		if p.Path == "/login" {
			require.True(t, p.Public)
			require.Equal(t, "login", p.Operation.Action)
		} else {
			require.False(t, p.Public)
		}
	}
}

func TestStartupErrorIdentifiesRoute(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	v := auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{}, principal.ErrUnauthenticated
	})
	for _, route := range []router.Route{
		{Method: "POST", Path: "/broken/{id", Handler: h},
		{Method: "POST", Path: "/broken", Handler: nil},
		{Method: "GET", Path: "/healthz", Handler: h},
		{Method: "GET", Path: "/broken/{id}", Public: true, Handler: h},
	} {
		r, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{Verifier: v, Routes: []router.Route{route}})
		require.Nil(t, r)
		require.ErrorContains(t, err, route.Method)
		require.ErrorContains(t, err, route.Path)
	}
}

func TestStartupDuplicatePublicPolicyIdentifiesRoute(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	v := auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{}, principal.ErrUnauthenticated
	})
	r, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{
		Verifier:        v,
		Routes:          []router.Route{{Method: "POST", Path: "/login", Public: true, Handler: h}},
		PublicEndpoints: []auth.Endpoint{{Method: "POST", Path: "/login"}},
	})
	require.Nil(t, r)
	require.ErrorContains(t, err, "POST")
	require.ErrorContains(t, err, "/login")
}

func TestCollectRejectsUnsafeModuleIdentifiers(t *testing.T) {
	for _, module := range []string{"", " ", " inventory", "inventory ", "inventory\nhttp", strings.Repeat("a", 129)} {
		routes, err := router.Collect(module)
		require.Nil(t, routes)
		require.Error(t, err)
	}
}

func TestBindRejectsExistingHandlerAndPreservesCause(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	cause := errors.New("binding failed")
	for _, bindingErr := range []error{nil, cause} {
		routes, err := router.Collect("inventory", router.Route{Method: "GET", Path: "/items", Handler: h}.Bind(h, bindingErr))
		require.Nil(t, routes)
		require.ErrorContains(t, err, "handler already set")
		if bindingErr != nil {
			require.ErrorIs(t, err, cause)
		}
	}
}

func TestStartupInvalidPublicEndpointIdentifiesRoute(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	v := auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{}, principal.ErrUnauthenticated
	})
	r, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{
		Verifier:        v,
		Routes:          []router.Route{{Method: "GET", Path: "/items/{id}", Handler: h}},
		PublicEndpoints: []auth.Endpoint{{Method: "GET", Path: "/items/{id}"}},
	})
	require.Nil(t, r)
	require.ErrorContains(t, err, "GET")
	require.ErrorContains(t, err, "/items/{id}")
}
