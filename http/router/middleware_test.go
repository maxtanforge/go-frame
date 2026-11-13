package router_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func actorVerifier(context.Context, string) (principal.Principal, error) {
	return principal.Principal{ID: "42", Name: "Alice", Issuer: "accounts"}, nil
}

func TestMiddlewareScopePhasesAndUnwind(t *testing.T) {
	var order []string
	entry := func(name string, phase router.Phase) router.Middleware {
		return router.Middleware{Name: name, Phase: phase, Wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				policy, ok := router.Policy(r.Context())
				if !ok || policy.Path != "/items/{id}" {
					t.Fatalf("policy: %+v %v", policy, ok)
				}
				policy.Operation.Action = "changed"
				fresh, _ := router.Policy(r.Context())
				if fresh.Operation.Action != "item.read" {
					t.Fatal("policy mutated")
				}
				if phase == router.AuthenticatedPhase || phase == router.BeforeHandler {
					p, err := principal.Require(r.Context())
					if err != nil || p.Name != "Alice" {
						t.Fatalf("actor: %+v %v", p, err)
					}
				}
				order = append(order, name+"+")
				defer func() { order = append(order, name+"-") }()
				next.ServeHTTP(w, r)
			})
		}}
	}
	audit, tenant := entry("audit", router.AuthenticatedPhase), entry("tenant", router.AuthenticatedPhase)
	audit.After = []string{"tenant"}
	routes, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{
		Router: router.Options{DisableHealthMount: true},
		Verifier: auth.VerifyFunc(func(ctx context.Context, raw string) (principal.Principal, error) {
			order = append(order, "verify")
			return actorVerifier(ctx, raw)
		}),
		Middleware: []router.Middleware{entry("response", router.Response), entry("matched", router.Matched), entry("app", router.AuthenticatedPhase)},
		Groups: []router.Group{{Name: "items", Middleware: []router.Middleware{audit, tenant}, Routes: []router.Route{{
			Method: "GET", Path: "/items/{id}", Operation: operationhttp.Metadata{Action: "item.read"},
			Middleware: []router.Middleware{entry("before", router.BeforeHandler)},
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if chi.URLParam(r, "id") != "7" {
					t.Fatal("missing params")
				}
				order = append(order, "handler")
				w.WriteHeader(204)
			}),
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Configuration slices are no longer read once registration has completed.
	audit.After[0] = "missing"
	r := httptest.NewRequest("GET", "/items/7", nil)
	r.Header.Set("Authorization", "Bearer valid")
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	want := []string{"response+", "matched+", "verify", "app+", "tenant+", "audit+", "before+", "handler", "before-", "audit-", "tenant-", "app-", "matched-", "response-"}
	if w.Code != 204 || !reflect.DeepEqual(order, want) {
		t.Fatalf("%d %v", w.Code, order)
	}
}

func TestMiddlewareRejectionRetainsActorAndAudit(t *testing.T) {
	var events []operation.Event
	var logs strings.Builder
	recorder, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, event operation.Event) error { events = append(events, event); return nil }), operationhttp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	routes, err := router.NewAuthenticated(slog.New(slog.NewJSONHandler(&logs, nil)), router.AuthenticatedOptions{
		Router: router.Options{DisableHealthMount: true}, Verifier: auth.VerifyFunc(actorVerifier), Operations: recorder,
		Middleware: []router.Middleware{{Name: "headers", Phase: router.Response, Wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Policy", "response")
				next.ServeHTTP(w, r)
			})
		}},
			{Name: "deny", Phase: router.AuthenticatedPhase, Wrap: func(http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					logger.FromContext(r.Context()).Info("business denied")
					w.WriteHeader(403)
				})
			}}},
		Routes: []router.Route{{Method: "POST", Path: "/items", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler ran") })}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"", "Bearer valid"} {
		r := httptest.NewRequest("POST", "/items", nil)
		if credential != "" {
			r.Header.Set("Authorization", credential)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		want := 401
		if credential != "" {
			want = 403
		}
		if w.Code != want || w.Header().Get("X-Policy") != "response" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
	}
	if len(events) != 2 || events[0].ActorID != "" || events[1].ActorID != "42" || events[1].ActorName != "Alice" || events[1].Outcome != operation.Denied {
		t.Fatalf("%+v", events)
	}
	if !strings.Contains(logs.String(), `"actor_name":"Alice"`) {
		t.Fatal(logs.String())
	}
}

func TestAuthenticationCheckPublishesVerifiedActorOnForbidden(t *testing.T) {
	var event operation.Event
	recorder, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	routes, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{Router: router.Options{DisableHealthMount: true}, Verifier: auth.VerifyFunc(actorVerifier), Operations: recorder,
		Check: func(ctx context.Context, actor principal.Principal) error {
			p, err := principal.Require(ctx)
			if err != nil || p != actor {
				t.Fatal("check lacks verified identity")
			}
			return &httperror.Error{Status: 403, Message: "Denied"}
		},
		Routes: []router.Route{{Method: "GET", Path: "/items", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler ran") })}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/items", nil)
	r.Header.Set("Authorization", "Bearer valid")
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	if w.Code != 403 || event.ActorName != "Alice" || event.ActorID != "42" {
		t.Fatalf("%d %+v", w.Code, event)
	}
}

func TestMiddlewareOrderingFailsAtStartup(t *testing.T) {
	wrap := func(next http.Handler) http.Handler { return next }
	for _, entries := range [][]router.Middleware{
		{{Name: "one", Phase: router.Matched}},
		{{Name: "one", Phase: 99, Wrap: wrap}},
		{{Name: "one", Phase: router.Matched, Wrap: wrap}, {Name: "one", Phase: router.Response, Wrap: wrap}},
		{{Name: "one", Phase: router.Matched, Wrap: wrap, Before: []string{"missing"}}},
		{{Name: "one", Phase: router.Matched, Wrap: wrap, Before: []string{"two"}}, {Name: "two", Phase: router.Response, Wrap: wrap}},
		{{Name: "one", Phase: router.Matched, Wrap: wrap, Before: []string{"two"}}, {Name: "two", Phase: router.Matched, Wrap: wrap, Before: []string{"one"}}},
		{{Name: "one", Phase: router.Response, Wrap: func(http.Handler) http.Handler { var h http.HandlerFunc; return h }}},
	} {
		_, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{Router: router.Options{DisableHealthMount: true}, Verifier: auth.VerifyFunc(actorVerifier), Middleware: entries, Routes: []router.Route{{Method: "GET", Path: "/items", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}}})
		if err == nil {
			t.Fatalf("accepted %+v", entries)
		}
	}
}

func TestMatchedMiddlewareCannotRetargetPublicRoute(t *testing.T) {
	for _, rewrite := range []func(*http.Request){func(r *http.Request) { r.URL.Path = "/private" }, func(r *http.Request) { r.Method = "POST" }, func(r *http.Request) { chi.RouteContext(r.Context()).RoutePath = "/private" }} {
		routes, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{Router: router.Options{DisableHealthMount: true}, Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
			return principal.Principal{}, errors.New("must not verify")
		}), Middleware: []router.Middleware{{Name: "rewrite", Phase: router.Matched, Wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rewrite(r); next.ServeHTTP(w, r) })
		}}}, Routes: []router.Route{
			{Method: "GET", Path: "/public", Public: true, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("public target altered") })},
			{Method: "GET", Path: "/private", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("private handler bypassed") })},
		}})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest("GET", "/public", nil))
		if w.Code < 400 {
			t.Fatalf("rewrite succeeded: %d", w.Code)
		}
	}
}

func TestResponseMiddlewarePreservesStreaming(t *testing.T) {
	routes, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{Router: router.Options{DisableHealthMount: true}, Verifier: auth.VerifyFunc(actorVerifier), Middleware: []router.Middleware{{Name: "response", Phase: router.Response, Wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Stream", "yes"); next.ServeHTTP(w, r) })
	}}}, Routes: []router.Route{{Method: "GET", Path: "/stream", Public: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, "data: last\n\n")
	})}}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, httptest.NewRequest("GET", "/stream", nil))
	if !w.Flushed || w.Body.String() != "data: first\n\ndata: last\n\n" || w.Header().Get("X-Stream") != "yes" {
		t.Fatalf("%+v", w)
	}
}

func TestMatchedMiddlewareCanRedirect(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			routes, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{
				Router: router.Options{DisableHealthMount: true}, Verifier: auth.VerifyFunc(actorVerifier),
				Middleware: []router.Middleware{{Name: "redirect", Phase: router.Matched, Wrap: func(http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						http.Redirect(w, r, "/new?from=old", status)
					})
				}}},
				Routes: []router.Route{{Method: "GET", Path: "/old", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("redirect continued") })}},
			})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, httptest.NewRequest("GET", "/old", nil))
			if w.Code != status || w.Header().Get("Location") != "/new?from=old" {
				t.Fatalf("%d %v", w.Code, w.Header())
			}
		})
	}
}

func TestResponseMiddlewareSeesRecoveredFailure(t *testing.T) {
	returned := false
	routes, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{Router: router.Options{DisableHealthMount: true}, Verifier: auth.VerifyFunc(actorVerifier), Middleware: []router.Middleware{{Name: "response", Phase: router.Response, Wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r); returned = true })
	}}}, Routes: []router.Route{{Method: "GET", Path: "/panic", Public: true, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private detail") })}}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, httptest.NewRequest("GET", "/panic", nil))
	if w.Code != 500 || !returned || strings.Contains(w.Body.String(), "private detail") {
		t.Fatalf("%d %t %s", w.Code, returned, w.Body)
	}
}
