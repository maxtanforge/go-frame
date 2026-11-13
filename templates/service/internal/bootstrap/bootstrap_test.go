package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/app/command"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/concurrency"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/id"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

// This is the authenticated replacement for handler, not a child of handler's
// framework router. Verify the complete request boundary used during migration.
func TestAuthenticatedRequestBoundaryConsumer(t *testing.T) {
	var logs bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&logs, nil))
	checker, err := health.New(health.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var observed int
	var requestTrace string
	routes, err := router.NewAuthenticated(lg, router.AuthenticatedOptions{
		Router: router.Options{
			Timeout: 2 * time.Minute, Health: checker,
			OnRequest: func(context.Context, middleware.RequestEvent) { observed++ },
		},
		Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
			return principal.Principal{ID: "42", Issuer: "accounts"}, nil
		}),
		PublicEndpoints: []auth.Endpoint{{Method: "GET", Path: "/healthz"}, {Method: "HEAD", Path: "/healthz"}, {Method: "GET", Path: "/readyz"}, {Method: "HEAD", Path: "/readyz"}},
		Routes: []router.Route{{Method: "GET", Path: "/probe", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) < 115*time.Second || time.Until(deadline) > 2*time.Minute {
				t.Errorf("configured request budget lost: %v, %v", deadline, ok)
			}
			requestTrace = trace.FromContext(r.Context())
			w.WriteHeader(http.StatusNoContent)
		})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, authorized := range []bool{false, true} {
		logs.Reset()
		observed = 0
		r := httptest.NewRequest("GET", "/probe", nil)
		if authorized {
			r.Header.Set("Authorization", "Bearer test")
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if authorized {
			want = http.StatusNoContent
		}
		if w.Code != want || observed != 1 {
			t.Fatalf("status=%d, observations=%d", w.Code, observed)
		}
		decoder := json.NewDecoder(&logs)
		var record struct {
			TraceID string `json:"trace_id"`
		}
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record.TraceID == "" || record.TraceID != w.Header().Get("X-Trace-ID") || authorized && record.TraceID != requestTrace {
			t.Fatalf("trace mismatch: log=%q response=%q handler=%q", record.TraceID, w.Header().Get("X-Trace-ID"), requestTrace)
		}
		if err := decoder.Decode(&record); err != io.EOF {
			t.Fatalf("expected one access log, got %v", err)
		}
	}
}

func TestUnusedProviderSettingsDoNotBlockStartupConfiguration(t *testing.T) {
	values := map[string]string{"DATABASE_DRIVER": "", "DB_MAX_OPEN_CONNS": "-1", "REDIS_POOL_SIZE": "-1"}
	_, err := loadSettings(nil, func(key string) (string, bool) { value, ok := values[key]; return value, ok })
	if err != nil {
		t.Fatalf("disabled provider blocked configuration: %v", err)
	}
	values["DATABASE_URL"] = "custom://configured"
	if _, err := loadSettings(nil, func(key string) (string, bool) { value, ok := values[key]; return value, ok }); err == nil {
		t.Fatal("enabled invalid database accepted")
	}
	delete(values, "DATABASE_URL")
	values["REDIS_ADDR"] = "localhost:6379"
	if _, err := loadSettings(nil, func(key string) (string, bool) { value, ok := values[key]; return value, ok }); err == nil {
		t.Fatal("enabled invalid Redis accepted")
	}
}

func TestExecutionBudgetConsumer(t *testing.T) {
	budget, err := concurrency.NewExecutor(concurrency.ExecutorOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = budget.Close() }()
	called := false
	if err := budget.Do(context.Background(), func(context.Context) error { called = true; return nil }); err != nil || !called {
		t.Fatal(err)
	}
	if err := budget.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNamedMiddlewareAndTypedResultConsumer(t *testing.T) {
	type input struct{ Name string }
	type result struct {
		Name string `json:"name"`
	}
	callback, err := binding.Intercept(func(_ context.Context, req *input) (*result, error) {
		return &result{Name: req.Name}, nil
	}, func(next binding.Handler[input, result]) binding.Handler[input, result] {
		return func(ctx context.Context, req *input) (*result, error) {
			value, err := next(ctx, req)
			if err == nil && value != nil {
				value.Name += ":wrapped"
			}
			return value, err
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := binding.Query(callback)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(io.Discard, nil)), router.AuthenticatedOptions{
		Router: router.Options{DisableHealthMount: true},
		Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
			return principal.Principal{ID: "42", Name: "Alice", Issuer: "accounts"}, nil
		}),
		Middleware: []router.Middleware{{Name: "headers", Phase: router.Response, Wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Response", "yes")
				next.ServeHTTP(w, r)
			})
		}}},
		Groups: []router.Group{{Name: "profile", Routes: []router.Route{{Method: "GET", Path: "/profile", Handler: endpoint}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, authorized := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/profile?name=Ada", nil)
		if authorized {
			r.Header.Set("Authorization", "Bearer test")
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if authorized {
			want = http.StatusOK
		}
		if w.Code != want || w.Header().Get("X-Response") != "yes" || authorized && !strings.Contains(w.Body.String(), "Ada:wrapped") {
			t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body)
		}
	}
}

func TestCanonicalIDBinding(t *testing.T) {
	type DTO struct{ ID id.ID }
	plan, err := request.Prepare[DTO](request.Options{Source: request.JSON})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"id":"9007199254740993"}`, `{"id":9007199254740993}`, `{"id":"01"}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		got, _, err := plan.Decode(httptest.NewRecorder(), r, nil)
		valid := body == `{"id":"9007199254740993"}`
		if (err == nil) != valid || valid && got.ID.Int64() != 9007199254740993 {
			t.Fatalf("%s: %d %v", body, got.ID, err)
		}
	}
}

func TestMetricsEndpointExplicitOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		loaded, err := loadSettings(nil, func(string) (string, bool) { return "", false })
		if err != nil {
			t.Fatal(err)
		}
		s := loaded.Value
		s.MetricsEnabled = enabled
		checker, err := health.New(health.Options{})
		if err != nil {
			t.Fatal(err)
		}
		ids, err := id.NewSnowflake(0)
		if err != nil {
			t.Fatal(err)
		}
		routes, err := handler(s, slog.New(slog.NewTextHandler(io.Discard, nil)), checker, ids)
		if err != nil {
			t.Fatal(err)
		}
		if err := composeFeatures(s, routes, dependencies{}); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
		if enabled {
			if w.Code != 200 || !strings.Contains(w.Body.String(), "go_goroutines") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		} else if w.Code != 404 {
			t.Fatalf("disabled: %d", w.Code)
		}
	}
}

func TestInspectionDoesNotStartRuntime(t *testing.T) {
	// Deliberately unusable dependencies and log paths: inspection must not open them.
	env := map[string]string{"DATABASE_URL": "postgres://user:secret@127.0.0.1:1/service", "DATABASE_DRIVER": "postgres", "REDIS_ADDR": "127.0.0.1:1", "REDIS_PASSWORD": "private-password", "LOG_OUTPUT": "file", "LOG_DIR": filepath.Join(t.TempDir(), "never-created")}
	lookup := func(key string) (string, bool) { v, ok := env[key]; return v, ok }
	for _, args := range [][]string{{"version"}, {"config", "check", "--config", "../../config.yaml"}, {"config", "explain", "--config", "../../config.yaml"}} {
		var out bytes.Buffer
		if err := Execute(context.Background(), args, &out, lookup, command.BuildInfo{Version: "test-version"}); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out.Len() == 0 || strings.Contains(out.String(), "private-password") || strings.Contains(out.String(), "postgres://") {
			t.Fatalf("unsafe or empty output: %s", out.String())
		}
	}
	if _, err := os.Stat(env["LOG_DIR"]); !os.IsNotExist(err) {
		t.Fatalf("inspection created log directory: %v", err)
	}
}

func TestHandler(t *testing.T) {
	settings, err := loadSettings(nil, func(key string) (string, bool) { return "42", key == "ID_NODE" })
	if err != nil {
		t.Fatal(err)
	}
	if settings.Value.Host != "127.0.0.1" {
		t.Fatal("default listener must be loopback")
	}
	checker, err := health.New(health.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := id.NewSnowflake(settings.Value.Framework.ID.Node)
	if err != nil {
		t.Fatal(err)
	}
	h, err := handler(settings.Value, slog.New(slog.NewTextHandler(io.Discard, nil)), checker, ids)
	if err != nil {
		t.Fatal(err)
	}
	if err := composeFeatures(settings.Value, h, dependencies{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"POST", "/greetings", `{"name":"Ada"}`, 200, `"message":"Hello, Ada"`},
		{"POST", "/greetings", `{"name":" "}`, 400, `"success":false`},
		{"POST", "/greetings", `{"name":"Ada","admin":true}`, 400, `"success":false`},
		{"GET", "/healthz", "", 200, `"status":"ok"`},
		{"GET", "/readyz", "", 200, `"status":"ready"`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		traceID, err := strconv.ParseInt(out.Header().Get("x-trace-id"), 10, 64)
		if err != nil || (traceID>>12)&1023 != 42 {
			t.Fatalf("configured trace node: %q %v", out.Header().Get("x-trace-id"), err)
		}
		if out.Code != tc.status || !strings.Contains(out.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %s", tc.path, out.Code, out.Body.String())
		}
	}
}

func TestHandlerParameterCaptureRequiresExplicitPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{{"disabled by default", false}, {"explicit field selection", true}} {
		t.Run(tc.name, func(t *testing.T) {
			settings, err := loadSettings(nil, func(string) (string, bool) { return "", false })
			if err != nil {
				t.Fatal(err)
			}
			checker, err := health.New(health.Options{})
			if err != nil {
				t.Fatal(err)
			}
			ids, err := id.NewSnowflake(0)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			routes, err := handler(settings.Value, slog.New(slog.NewJSONHandler(&logs, nil)), checker, ids)
			if err != nil {
				t.Fatal(err)
			}
			if err := composeFeatures(settings.Value, routes, dependencies{}); err != nil {
				t.Fatal(err)
			}
			path := "/greetings?tag=one&tag=two&password=private"
			if tc.enabled {
				routes.With(operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{
					Query: operationhttp.Enabled, Params: operationhttp.Enabled, Body: operationhttp.Enabled,
					QueryFields: []string{"tag"}, ParamsFields: []string{"id"}, BodyFields: []string{"name"},
				}})).Post("/capture/{id}", func(_ http.ResponseWriter, r *http.Request) {
					_, err := io.Copy(io.Discard, r.Body)
					if err != nil {
						t.Fatal(err)
					}
				})
				path = "/capture/42?tag=one&tag=two&password=private"
			}
			req := httptest.NewRequest("POST", path, strings.NewReader(`{"name":"Ada","email":"private"}`))
			if !tc.enabled {
				req = httptest.NewRequest("POST", path, strings.NewReader(`{"name":"Ada"}`))
			}
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			routes.ServeHTTP(response, req)
			if response.Code != 200 {
				t.Fatalf("response: %d %s", response.Code, response.Body.String())
			}
			decoder := json.NewDecoder(&logs)
			operations := 0
			for decoder.More() {
				var record struct {
					Kind  string `json:"kind"`
					Event struct {
						Path    string `json:"path"`
						Request struct {
							Query  map[string][]string `json:"query"`
							Body   map[string]string   `json:"body"`
							Params map[string]string   `json:"params"`
						} `json:"request"`
					} `json:"event"`
				}
				if err := decoder.Decode(&record); err != nil {
					t.Fatal(err)
				}
				if record.Kind != "operation" {
					continue
				}
				operations++
				if !tc.enabled {
					if record.Event.Path != "/greetings" || len(record.Event.Request.Body)+len(record.Event.Request.Query)+len(record.Event.Request.Params) != 0 {
						t.Fatalf("parameters captured by default: %+v", record)
					}
				} else if record.Event.Path != "/capture/42" || record.Event.Request.Body["name"] != "Ada" || record.Event.Request.Params["id"] != "42" || strings.Join(record.Event.Request.Query["tag"], ",") != "one,two" || len(record.Event.Request.Body) != 1 || len(record.Event.Request.Query) != 1 || len(record.Event.Request.Params) != 1 {
					t.Fatalf("operation: %+v", record)
				}
			}
			if operations != 1 {
				t.Fatalf("operations: %d", operations)
			}
		})
	}
}
