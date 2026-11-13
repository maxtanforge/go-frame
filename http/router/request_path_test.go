package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/stretchr/testify/require"
)

func TestRequestLogsKeepConcretePathAndSnowflakeTrace(t *testing.T) {
	for _, target := range []string{"/users/42?token=hidden", "/users/a%2Fb?token=hidden", "/missing?token=hidden", "/" + strings.Repeat("x", 2049)} {
		t.Run(target[:min(32, len(target))], func(t *testing.T) {
			var output bytes.Buffer
			var access middleware.RequestEvent
			var event operation.Event
			lg := slog.New(slog.NewJSONHandler(&output, nil))
			sink := operation.NewSlogSink(lg)
			observer, err := operationhttp.New(operation.SinkFunc(func(ctx context.Context, value operation.Event) error {
				event = value
				return sink.Record(ctx, value)
			}), operationhttp.Options{})
			require.NoError(t, err)
			r := router.New(lg, router.Options{
				Middleware: []func(http.Handler) http.Handler{observer.Middleware},
				OnRequest:  func(_ context.Context, value middleware.RequestEvent) { access = value },
			})
			r.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
			response := httptest.NewRecorder()
			r.ServeHTTP(response, httptest.NewRequest("GET", target, nil))
			path := strings.SplitN(target, "?", 2)[0]
			if len(path) > 2048 {
				path = "<path>"
			}
			require.Equal(t, path, access.Path)
			require.Equal(t, path, event.Path)
			require.Equal(t, access.Route, event.Route)
			traceID := response.Header().Get("x-trace-id")
			n, err := strconv.ParseInt(traceID, 10, 64)
			require.NoError(t, err)
			require.Positive(t, n)
			require.Equal(t, traceID, event.TraceID)
			require.NotContains(t, output.String(), "hidden")
			decoder := json.NewDecoder(&output)
			for i := 0; i < 2; i++ {
				var log map[string]any
				require.NoError(t, decoder.Decode(&log))
				if log["kind"] == "operation" {
					require.Equal(t, path, log["event"].(map[string]any)["path"])
				} else {
					require.Equal(t, path, log["path"])
				}
			}
		})
	}
}
