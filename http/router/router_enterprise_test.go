package router

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/respond"
)

func TestMethodNotAllowedUsesEffectiveRoutePath(t *testing.T) {
	for _, tc := range []struct {
		name, path      string
		normalize       func(http.Handler) http.Handler
		nested, mounted bool
	}{
		{"strip", "/users/42/", chimiddleware.StripSlashes, false, false},
		{"clean", "/users/../users/42", chimiddleware.CleanPath, false, false},
		{"nested strip", "/admin/users/42/", chimiddleware.StripSlashes, true, false},
		{"mounted nested strip", "/v1/admin/users/42/", chimiddleware.StripSlashes, true, true},
		{"encoded", "/users/a%2Fb", nil, false, false},
		{"mounted encoded strip", "/v1/admin/users/a%2Fb/", chimiddleware.StripSlashes, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{}
			if !tc.nested && tc.normalize != nil {
				opts.Middleware = []func(http.Handler) http.Handler{tc.normalize}
			}
			r := New(nil, opts)
			if tc.nested {
				// A matching path on a different router must not pollute Allow.
				r.Delete("/users/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
			}
			register := func(child chi.Router) {
				if tc.nested && tc.normalize != nil {
					child.Use(tc.normalize)
				}
				child.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
			}
			if tc.nested {
				r.Route("/admin", register)
			} else {
				register(r)
			}
			var handler http.Handler = r
			if tc.mounted {
				parent := chi.NewRouter()
				parent.Mount("/v1", r)
				handler = parent
			}
			for _, method := range []string{"GET", "POST"} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(method, tc.path, nil))
				if method == "GET" {
					if w.Code != 204 {
						t.Fatalf("GET: %d", w.Code)
					}
				} else if w.Code != 405 || w.Header().Get("Allow") != "GET" {
					t.Fatalf("POST: %d Allow=%q", w.Code, w.Header().Get("Allow"))
				}
			}
		})
	}
}

func TestMethodNotAllowedAtMountedRoot(t *testing.T) {
	r := New(nil, Options{})
	r.Route("/admin", func(child chi.Router) {
		child.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	})
	parent := chi.NewRouter()
	parent.Mount("/v1/{tenant}", r)
	for _, path := range []string{"/v1/acme/admin", "/v1/acme/admin/"} {
		w := httptest.NewRecorder()
		parent.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 405 || w.Header().Get("Allow") != "GET" {
			t.Fatalf("%s: %d Allow=%q", path, w.Code, w.Header().Get("Allow"))
		}
	}
}

func TestBusinessRoutesAndFrameworkErrors(t *testing.T) {
	var logs bytes.Buffer
	var event middleware.RequestEvent
	r := New(slog.New(slog.NewJSONHandler(&logs, nil)), Options{OnRequest: func(_ context.Context, e middleware.RequestEvent) { event = e }})
	r.Get("/users/{id}", respond.Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) { return map[string]string{"id": "42"}, nil }))
	r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("private panic") })
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/users/42", 200}, {"POST", "/users/42", 405}, {"GET", "/unknown", 404}, {"GET", "/panic", 500}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path+"?secret=hide", nil)
		req.Header.Set("x-trace-id", "test-trace")
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		var env respond.Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.TraceID != "test-trace" || event.Status != tc.status || event.Path != tc.path {
			t.Fatalf("env=%+v event=%+v", env, event)
		}
		if strings.Contains(w.Body.String(), "private panic") {
			t.Fatal("panic leaked")
		}
		if tc.status == 200 && event.Route != "/users/{id}" {
			t.Fatalf("route=%s", event.Route)
		}
	}
	if strings.Contains(logs.String(), "secret=hide") {
		t.Fatal("query logged")
	}
}

func TestRouterDeadlineAndStreaming(t *testing.T) {
	r := New(nil, Options{Timeout: time.Millisecond})
	r.Get("/slow", respond.Adapter(func(_ http.ResponseWriter, r *http.Request) (any, error) { <-r.Context().Done(); return nil, nil }))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/slow", nil))
	if w.Code != 504 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	r = New(nil, Options{Timeout: -1})
	r.Get("/events", respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if _, ok := r.Context().Deadline(); ok {
			t.Error("stream has default deadline")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		return nil, http.NewResponseController(w).Flush()
	}))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/events", nil))
	if w.Body.Len() != 0 {
		t.Fatalf("stream polluted: %s", w.Body.String())
	}
}

func TestNativePanicAfterDeadlineIsGatewayTimeout(t *testing.T) {
	r := New(nil, Options{Timeout: time.Millisecond})
	r.Get("/native", func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done(); panic("late native panic") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/native", nil))
	if w.Code != 504 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestMethodNotAllowedIncludesAllowedMethods(t *testing.T) {
	r := New(nil, Options{})
	r.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	r.Route("/admin", func(child chi.Router) {
		child.Put("/orders/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	})
	for _, tc := range []struct{ path, want string }{{"/healthz", "GET, HEAD"}, {"/users/42", "GET"}, {"/admin/orders/1", "PUT"}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", tc.path, nil))
		if w.Code != 405 || w.Header().Get("Allow") != tc.want {
			t.Fatalf("%s: %d Allow=%q", tc.path, w.Code, w.Header().Get("Allow"))
		}
	}
	parent := chi.NewRouter()
	parent.Mount("/v1", r)
	w := httptest.NewRecorder()
	parent.ServeHTTP(w, httptest.NewRequest("POST", "/v1/admin/orders/1", nil))
	if w.Header().Get("Allow") != "PUT" {
		t.Fatalf("mounted Allow=%q", w.Header().Get("Allow"))
	}
}
