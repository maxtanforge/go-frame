package router_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

func TestAuthenticatedRouteComposition(t *testing.T) {
	var events []operation.Event
	rec, _ := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { events = append(events, e); return nil }), operationhttp.Options{})
	v := auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{ID: "42", Issuer: "test"}, nil
	})
	routes, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{Verifier: v, Operations: rec, Routes: []router.Route{
		{Method: "POST", Path: "/login", Public: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), Operation: operationhttp.Metadata{Action: "login"}},
		{Method: "GET", Path: "/me", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"POST", "/login", 204}, {"GET", "/me", 401}, {"GET", "/new", 401}, {"GET", "/healthz", 401}} {
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.code {
			t.Fatalf("%s %d", tc.path, w.Code)
		}
	}
	if len(events) != 4 || events[0].Action != "login" || events[1].Outcome != operation.Denied {
		t.Fatalf("%+v", events)
	}
	policy := routes.Policies()
	if len(policy) != 6 {
		t.Fatalf("policies %+v", policy)
	}
}
func TestInvalidRouteDeclarations(t *testing.T) {
	v := auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) { return principal.Principal{}, nil })
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, rs := range [][]router.Route{{{Method: "GET", Path: "/a", Handler: h}, {Method: "GET", Path: "/a", Handler: h}}, {{Method: "GET", Path: "/x/{id}", Handler: h, Public: true}}, {{Method: "GET", Path: "/bad", Handler: nil}}} {
		if _, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{Verifier: v, Routes: rs}); err == nil {
			t.Fatal("invalid routes accepted")
		}
	}
}

func TestLowercaseMethodCannotOverwritePublicRoute(t *testing.T) {
	v := auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{}, principal.ErrUnauthenticated
	})
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	_, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{Verifier: v, Routes: []router.Route{
		{Method: "GET", Path: "/same", Public: true, Handler: h},
		{Method: "get", Path: "/same", Handler: h},
	}})
	if err == nil {
		t.Fatal("lowercase method overwrote public handler")
	}
}

func TestDeniedDynamicRouteKeepsTemplate(t *testing.T) {
	var event operation.Event
	rec, _ := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	r, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{}, principal.ErrUnauthenticated
	}), Operations: rec})
	if err != nil {
		t.Fatal(err)
	}
	r.Get("/new/{id}", func(http.ResponseWriter, *http.Request) { t.Fatal("unauthenticated handler called") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/new/private-id?token=hidden", nil))
	if w.Code != 401 || event.Route != "/new/{id}" || event.Path != "/new/private-id" || event.Action != "GET /new/{id}" {
		t.Fatalf("%+v", event)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/new/another-id", nil))
	if w.Code != 401 || event.Route != "/new/{id}" || event.Path != "/new/another-id" || event.Action != "GET /new/{id}" {
		t.Fatalf("cached route policy: %d %+v", w.Code, event)
	}
}

func BenchmarkAuthenticatedDeclaredPublicRoute(b *testing.B) {
	v := auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		b.Fatal("public route called verifier")
		return principal.Principal{}, nil
	})
	r, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{
		Verifier: v,
		Routes: []router.Route{{Method: "GET", Path: "/public", Public: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })}},
	})
	if err != nil {
		b.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/public", nil)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 204 {
			b.Fatal(w.Code)
		}
	}
}
