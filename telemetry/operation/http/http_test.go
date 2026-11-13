package operationhttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

func TestAutomaticOperationIdentityAndMetadata(t *testing.T) {
	var events []operation.Event
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { events = append(events, e); return nil }), operationhttp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := auth.New(auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{ID: "42", Issuer: "test"}, nil
	}), auth.Options{})
	r := chi.NewRouter()
	r.Use(rec.Middleware)
	r.Use(a.Middleware)
	r.With(operationhttp.With(operationhttp.Metadata{Action: "order.create", Description: "Create order"})).Post("/orders", func(w http.ResponseWriter, r *http.Request) {
		if err := operationhttp.SetResource(r.Context(), "order", "123"); err != nil {
			t.Error(err)
		}
		if err := operationhttp.SetFields(r.Context(), map[string]any{"count": 1}); err != nil {
			t.Error(err)
		}
		w.WriteHeader(201)
	})
	req := httptest.NewRequest("POST", "/orders", nil)
	req.Header.Set("Authorization", "Bearer good")
	r.ServeHTTP(httptest.NewRecorder(), req)
	if len(events) != 1 || events[0].ActorID != "42" || events[0].Action != "order.create" || events[0].ResourceID != "123" || events[0].Status != 201 || events[0].Outcome != operation.Completed {
		t.Fatalf("%+v", events)
	}
}
func TestSinkFailurePanicAndCancellation(t *testing.T) {
	for _, mode := range []string{"sink error", "sink panic", "handler panic", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			var event operation.Event
			rec, _ := operationhttp.New(operation.SinkFunc(func(ctx context.Context, e operation.Event) error {
				calls++
				event = e
				if ctx.Err() != nil {
					t.Error("canceled sink context")
				}
				if mode == "sink panic" {
					panic("sink")
				}
				return errors.New("sink failed")
			}), operationhttp.Options{})
			h := respond.Recoverer(rec.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if mode == "handler panic" {
					panic("handler")
				}
				if mode != "cancel" {
					w.WriteHeader(204)
				}
			})))
			req := httptest.NewRequest("GET", "/", nil)
			if mode == "cancel" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if calls != 1 {
				t.Fatal(calls)
			}
			if mode == "handler panic" && event.Outcome != operation.Aborted {
				t.Fatal(event)
			}
			if mode == "cancel" && event.Outcome != operation.Canceled {
				t.Fatal(event)
			}
			if (mode == "sink error" || mode == "sink panic") && w.Code != 204 {
				t.Fatal(w.Code)
			}
		})
	}
}
func TestNestedAndDisabled(t *testing.T) {
	n := 0
	rec, _ := operationhttp.New(operation.SinkFunc(func(context.Context, operation.Event) error { n++; return nil }), operationhttp.Options{})
	h := rec.Middleware(rec.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if n != 1 {
		t.Fatal(n)
	}
	rec.Middleware(operationhttp.With(operationhttp.Metadata{Mode: operationhttp.Disabled})(h)).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if n != 1 {
		t.Fatal(n)
	}
}
func TestFlushPreserved(t *testing.T) {
	var e operation.Event
	rec, _ := operationhttp.New(operation.SinkFunc(func(_ context.Context, event operation.Event) error { e = event; return nil }), operationhttp.Options{})
	w := httptest.NewRecorder()
	rec.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
	})).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !w.Flushed || e.Status != 200 {
		t.Fatalf("%+v", e)
	}
}

func TestExpiredContextDoesNotReportCompleted(t *testing.T) {
	var event operation.Event
	rec, _ := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	rec.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	if event.Outcome != operation.Failed || event.Status != 0 {
		t.Fatalf("%+v", event)
	}
}
