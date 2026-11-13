package router_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/stretchr/testify/require"
)

func TestDeclaredInterfacesControlRequestLogging(t *testing.T) {
	var events []operation.Event
	recorder, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, event operation.Event) error {
		events = append(events, event)
		return nil
	}), operationhttp.Options{})
	require.NoError(t, err)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Equal(t, `{"name":"Ada","password":"hidden"}`, string(body))
		}
		w.WriteHeader(204)
	})
	verifier := auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{}, principal.ErrUnauthenticated
	})
	routes, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{
		Verifier: verifier, Operations: recorder,
		Routes: []router.Route{
			{Method: "POST", Path: "/login", Public: true, Handler: handler, Operation: operationhttp.Metadata{Request: operationhttp.RequestOptions{Query: operationhttp.Disabled}}},
			{Method: "GET", Path: "/search", Public: true, Handler: handler, Operation: operationhttp.Metadata{Request: operationhttp.RequestOptions{Body: operationhttp.Disabled}}},
			{Method: "POST", Path: "/plain", Public: true, Handler: handler},
			{Method: "POST", Path: "/private", Handler: handler, Operation: operationhttp.Metadata{Request: operationhttp.RequestOptions{Query: operationhttp.Enabled, Body: operationhttp.Enabled}}},
		},
	})
	require.NoError(t, err)
	for _, tc := range []struct {
		method, target, request string
		status                  int
	}{
		{"POST", "/login?token=hidden", `{"body":{"name":"Ada","password":"***"}}`, 204},
		{"GET", "/search?page=1&token=hidden", `{"query":{"page":["1"],"token":["***"]}}`, 204},
		{"POST", "/plain?token=hidden", `{"query":{"token":["***"]},"body":{"name":"Ada","password":"***"}}`, 204},
		{"POST", "/private?page=1&token=hidden", `{"query":{"page":["1"],"token":["***"]},"body_omitted":"not_read"}`, 401},
	} {
		source := strings.NewReader(`{"name":"Ada","password":"hidden"}`)
		request := httptest.NewRequest(tc.method, tc.target, source)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		require.Equal(t, tc.status, response.Code)
		event := events[len(events)-1]
		if tc.request == "" {
			require.Empty(t, event.Request)
		} else {
			require.JSONEq(t, tc.request, string(event.Request))
		}
		if tc.status == 401 {
			require.Equal(t, len(`{"name":"Ada","password":"hidden"}`), source.Len(), "denied request body must remain unread")
		}
	}
	require.Len(t, events, 4)
}

func TestRequestPoliciesAreIndependentSnapshots(t *testing.T) {
	var event operation.Event
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	require.NoError(t, err)
	options := operationhttp.RequestOptions{Query: operationhttp.Enabled, QueryFields: []string{"phone"}, SensitiveFields: []string{"phone"}}
	routes, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{
		Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
			return principal.Principal{}, principal.ErrUnauthenticated
		}),
		Operations: rec,
		Routes:     []router.Route{{Method: "GET", Path: "/search", Public: true, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Operation: operationhttp.Metadata{Request: options}}},
	})
	require.NoError(t, err)
	options.QueryFields[0], options.SensitiveFields[0] = "other", "other"
	for _, policy := range routes.Policies() {
		if policy.Path == "/search" {
			policy.Operation.Request.QueryFields[0] = "other"
			policy.Operation.Request.SensitiveFields[0] = "other"
		}
	}
	routes.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/search?phone=private&other=private", nil))
	require.JSONEq(t, `{"query":{"phone":["***"]}}`, string(event.Request))
}

func TestRequestDurationExcludesOperationSinkWork(t *testing.T) {
	var access middleware.RequestEvent
	var sinkWork time.Duration
	rec, err := operationhttp.New(operation.SinkFunc(func(context.Context, operation.Event) error {
		started := time.Now()
		time.Sleep(50 * time.Millisecond)
		sinkWork = time.Since(started)
		return nil
	}), operationhttp.Options{})
	require.NoError(t, err)
	routes := router.New(slog.New(slog.NewTextHandler(io.Discard, nil)), router.Options{
		Middleware: []func(http.Handler) http.Handler{rec.Middleware},
		OnRequest:  func(_ context.Context, event middleware.RequestEvent) { access = event },
	})
	routes.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	started := time.Now()
	routes.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	elapsed := time.Since(started)
	require.GreaterOrEqual(t, elapsed-access.Duration, sinkWork, "request duration included log sink work")
}

func TestInvalidRequestLoggingPolicyFailsAtStartup(t *testing.T) {
	_, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{
		Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
			return principal.Principal{}, principal.ErrUnauthenticated
		}),
		Routes: []router.Route{{Method: "POST", Path: "/bad", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Operation: operationhttp.Metadata{
			Request: operationhttp.RequestOptions{Body: operationhttp.Mode(255)},
		}}},
	})
	require.Error(t, err)
}

func TestRouteParamsCaptureBeforeAndAfterAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name       string
		authorized bool
		options    operationhttp.RequestOptions
		want       string
	}{
		{"allowed", true, operationhttp.RequestOptions{}, `{"query":{},"params":{"id":"42","token":"***"}}`},
		{"denied", false, operationhttp.RequestOptions{}, `{"query":{},"params":{"id":"42","token":"***"}}`},
		{"selected", false, operationhttp.RequestOptions{ParamsFields: []string{"id"}}, `{"query":{},"params":{"id":"42"}}`},
		{"disabled", true, operationhttp.RequestOptions{Params: operationhttp.Disabled}, `{"query":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event operation.Event
			rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
			require.NoError(t, err)
			routes, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{
				Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
					return principal.Principal{ID: "user", Issuer: "test"}, nil
				}), Operations: rec,
				Routes: []router.Route{{Method: "GET", Path: "/orders/{id}/{token}", Operation: operationhttp.Metadata{Request: tc.options}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "42", chi.URLParam(r, "id"))
					w.WriteHeader(204)
				})}},
			})
			require.NoError(t, err)
			req := httptest.NewRequest("GET", "/orders/42/private", nil)
			if tc.authorized {
				req.Header.Set("Authorization", "Bearer valid")
			}
			out := httptest.NewRecorder()
			routes.ServeHTTP(out, req)
			if tc.authorized {
				require.Equal(t, 204, out.Code)
			} else {
				require.Equal(t, 401, out.Code)
			}
			require.JSONEq(t, tc.want, string(event.Request))
		})
	}
}

func TestMountedRouteParamsAndOversizedValues(t *testing.T) {
	var event operation.Event
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	require.NoError(t, err)
	routes := router.New(slog.New(slog.NewTextHandler(io.Discard, nil)), router.Options{Middleware: []func(http.Handler) http.Handler{rec.Middleware}})
	routes.Route("/teams/{team}", func(child chi.Router) {
		child.Get("/orders/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	})
	routes.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/teams/one/orders/42", nil))
	require.JSONEq(t, `{"query":{},"params":{"team":"one","id":"42"}}`, string(event.Request))
	routes.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/teams/one/orders/"+strings.Repeat("x", 17<<10), nil))
	require.JSONEq(t, `{"query":{},"params_omitted":"too_large"}`, string(event.Request))
}
