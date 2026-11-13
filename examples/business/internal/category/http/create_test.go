package categoryhttp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

type createBody struct {
	io.Reader
	reads  int
	onRead func()
}

func (b *createBody) Read(p []byte) (int, error) {
	b.reads++
	if b.onRead != nil {
		b.onRead()
	}
	return b.Reader.Read(p)
}
func (*createBody) Close() error { return nil }

func runCreate(ctx context.Context, t *testing.T, path string, body *createBody, create CreateUseCase, record bool) (*httptest.ResponseRecorder, operation.Event) {
	t.Helper()
	h, err := CreateHandler(create)
	if err != nil {
		t.Fatal(err)
	}
	var event operation.Event
	if record {
		recorder, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
		if err != nil {
			t.Fatal(err)
		}
		h = recorder.Middleware(operationhttp.With(operationhttp.Metadata{Action: "category.create", Route: "/books/{bookId}/categories"})(h))
	}
	router := chi.NewRouter()
	router.Method(http.MethodPost, "/books/{bookId}/categories", h)
	r := httptest.NewRequest(http.MethodPost, "/books/"+path+"/categories", nil).WithContext(ctx)
	r.Body = body
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w, event
}

func TestCreateAuditFailures(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, kind, id string
		failure                    error
		status, calls              int
	}{
		{"path before body", "01", `{"name":"new"}`, "", "", nil, 400, 0},
		{"malformed body retains parent", "7", `{`, "book", "7", nil, 400, 0},
		{"missing name retains parent", "7", `{}`, "book", "7", nil, 400, 0},
		{"business failure retains parent", "7", `{"name":"new"}`, "book", "7", category.ErrForbidden, 403, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &createBody{Reader: strings.NewReader(tc.body)}
			calls := 0
			w, event := runCreate(context.Background(), t, tc.path, body, func(context.Context, string, category.ID, string) (category.Model, error) {
				calls++
				return category.Model{}, tc.failure
			}, true)
			if w.Code != tc.status || calls != tc.calls || event.ResourceType != tc.kind || event.ResourceID != tc.id {
				t.Fatalf("status=%d calls=%d event=%+v body=%s", w.Code, calls, event, w.Body.String())
			}
			if tc.path == "01" && body.reads != 0 {
				t.Fatal("invalid parent read body")
			}
			if tc.calls == 0 && strings.Contains(w.Body.String(), `"code":40001`) {
				t.Fatal("input failure mapped as business error")
			}
		})
	}
}

func TestCreateSuccessReplacesParent(t *testing.T) {
	ctx := principal.IntoContext(context.Background(), principal.Principal{ID: "alice", Issuer: "test"})
	id, _ := category.NewID(42)
	w, event := runCreate(ctx, t, "7", &createBody{Reader: strings.NewReader(`{"name":"new"}`)}, func(ctx context.Context, actor string, parent category.ID, name string) (category.Model, error) {
		if actor != "alice" || parent.String() != "7" || name != "new" {
			t.Fatalf("actor=%s parent=%s name=%s", actor, parent, name)
		}
		if _, ok := principal.FromContext(ctx); !ok {
			t.Fatal("principal lost")
		}
		return category.Model{ID: id, Name: name, Owner: "private"}, nil
	}, true)
	if w.Code != 200 || event.ResourceType != "category" || event.ResourceID != "42" || !strings.Contains(w.Body.String(), `"id":"42"`) || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("%d %s %+v", w.Code, w.Body.String(), event)
	}
}

func TestCreateCancellationStopsLaterStages(t *testing.T) {
	for _, phase := range []string{"before path", "during body", "after create"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &createBody{Reader: strings.NewReader(`{"name":"new"}`)}
			if phase == "before path" {
				cancel()
			}
			if phase == "during body" {
				body.onRead = cancel
			}
			calls := 0
			id, _ := category.NewID(42)
			w, event := runCreate(ctx, t, "7", body, func(context.Context, string, category.ID, string) (category.Model, error) {
				calls++
				cancel()
				return category.Model{ID: id}, nil
			}, true)
			if event.ResourceType == "category" || w.Body.Len() != 0 || event.Outcome != operation.Canceled {
				t.Fatalf("%s %+v", w.Body.String(), event)
			}
			if phase == "before path" && (body.reads != 0 || event.ResourceID != "") {
				t.Fatalf("reads=%d event=%+v", body.reads, event)
			}
			if phase != "before path" && event.ResourceID != "7" {
				t.Fatalf("parent lost: %+v", event)
			}
			wantCalls := 0
			if phase == "after create" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("create calls=%d", calls)
			}
		})
	}
}

func TestCreateRequiresRecorderAndValidResult(t *testing.T) {
	for _, record := range []bool{false, true} {
		body := &createBody{Reader: strings.NewReader(`{"name":"new"}`)}
		calls := 0
		w, event := runCreate(context.Background(), t, "7", body, func(context.Context, string, category.ID, string) (category.Model, error) {
			calls++
			return category.Model{}, nil
		}, record)
		if w.Code != 500 || strings.Contains(w.Body.String(), "recorder missing") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if !record && (calls != 0 || body.reads != 0) {
			t.Fatalf("calls=%d reads=%d", calls, body.reads)
		}
		if record && (calls != 1 || event.ResourceType != "book" || event.ResourceID != "7") {
			t.Fatalf("calls=%d event=%+v", calls, event)
		}
	}
	if _, err := CreateHandler(nil); err == nil {
		t.Fatal("nil use case accepted")
	}
}

func TestCreateCancellationRetainsBusinessFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var diagnostics strings.Builder
	ctx = logger.IntoContext(ctx, slog.New(slog.NewTextHandler(&diagnostics, nil)))
	cause := errors.New("create storage failed")
	w, event := runCreate(ctx, t, "7", &createBody{Reader: strings.NewReader(`{"name":"new"}`)}, func(context.Context, string, category.ID, string) (category.Model, error) {
		cancel()
		return category.Model{}, cause
	}, true)
	if w.Body.Len() != 0 || event.ResourceType != "book" || event.ResourceID != "7" || event.Outcome != operation.Canceled {
		t.Fatalf("body=%s event=%+v", w.Body.String(), event)
	}
	// Adapter logs independent failures even when request cancellation suppresses
	// the public response. Pure cancellation would suppress this error-level log.
	if !strings.Contains(diagnostics.String(), cause.Error()) || !strings.Contains(diagnostics.String(), context.Canceled.Error()) {
		t.Fatalf("lost simultaneous failure/cancellation: %s", diagnostics.String())
	}
}

func TestCreateMetadataWithoutRecorderFailsBeforeBody(t *testing.T) {
	calls := 0
	id, _ := category.NewID(42)
	h, err := CreateHandler(func(context.Context, string, category.ID, string) (category.Model, error) {
		calls++
		return category.Model{ID: id}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Method(http.MethodPost, "/books/{bookId}/categories", operationhttp.With(operationhttp.Metadata{Action: "category.create"})(h))
	body := &createBody{Reader: strings.NewReader(`{"name":"new"}`)}
	r := httptest.NewRequest(http.MethodPost, "/books/7/categories", nil)
	r.Body = body
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 500 || body.reads != 0 || calls != 0 || strings.Contains(w.Body.String(), "recorder missing") {
		t.Fatalf("status=%d reads=%d calls=%d body=%s", w.Code, body.reads, calls, w.Body.String())
	}
}
