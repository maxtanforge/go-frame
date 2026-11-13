package operationhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

func TestResourceAndFieldsRequireRecorder(t *testing.T) {
	for _, setter := range []struct {
		name string
		set  func(context.Context) error
	}{
		{"resource", func(ctx context.Context) error { return operationhttp.SetResource(ctx, "book", "42") }},
		{"fields", func(ctx context.Context) error { return operationhttp.SetFields(ctx, map[string]any{"count": 1}) }},
	} {
		t.Run(setter.name, func(t *testing.T) {
			if err := setter.set(context.Background()); err == nil {
				t.Fatal("missing recorder accepted")
			}
			h := operationhttp.With(operationhttp.Metadata{Action: "book.create"})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if err := setter.set(r.Context()); err == nil {
					t.Error("metadata alone accepted as recorder")
				}
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
		})
	}
}

func TestResourceAndFieldsWithRecorderInsideMetadata(t *testing.T) {
	var events []operation.Event
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error {
		events = append(events, e)
		return nil
	}), operationhttp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := operationhttp.With(operationhttp.Metadata{Action: "book.create"})(rec.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := operationhttp.SetResource(r.Context(), "book", "42"); err != nil {
			t.Fatal(err)
		}
		if err := operationhttp.SetFields(r.Context(), map[string]any{"count": 1}); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	if len(events) != 1 || events[0].ResourceID != "42" || events[0].ResourceType != "book" || events[0].Fields["count"] != 1 {
		t.Fatalf("events = %+v", events)
	}
}
