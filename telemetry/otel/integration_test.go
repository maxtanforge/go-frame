package telemetry_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/client"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/otel"
	"github.com/maxtanforge/go-frame/telemetry/trace"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestFrameworkRouterClientAndAccessLogShareTrace(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	adapter, err := telemetry.New(telemetry.Options{TracerProvider: provider})
	require.NoError(t, err)
	seen := make(chan [2]string, 1)
	downstream := httptest.NewServer(adapter.Handler("downstream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- [2]string{r.Header.Get(trace.HeaderTraceID), oteltrace.SpanContextFromContext(r.Context()).TraceID().String()}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})))
	t.Cleanup(downstream.Close)
	client, err := httpclient.New(httpclient.Options{WrapTransport: adapter.Transport})
	require.NoError(t, err)
	t.Cleanup(client.CloseIdleConnections)
	var logs bytes.Buffer
	lg := slog.New(telemetry.LogHandler(slog.NewJSONHandler(&logs, nil)))
	var observed string
	router := router.New(lg, router.Options{Middleware: []func(http.Handler) http.Handler{telemetry.Route}, OnRequest: func(ctx context.Context, e middleware.RequestEvent) {
		observed = oteltrace.SpanContextFromContext(ctx).TraceID().String()
		require.Equal(t, "/orders", e.Route)
	}})
	router.Get("/orders", respond.Adapter(func(_ http.ResponseWriter, r *http.Request) (any, error) {
		logger.FromContext(r.Context()).InfoContext(r.Context(), "business operation")
		var result map[string]bool
		err := httpclient.DoJSON(r.Context(), client, "GET", downstream.URL, nil, &result)
		return result, err
	}))
	request := httptest.NewRequest("GET", "/orders", nil)
	const traceID = "0123456789abcdef0123456789abcdef"
	request.Header.Set("traceparent", "00-"+traceID+"-0123456789abcdef-01")
	request.Header.Set(trace.HeaderTraceID, "correlation")
	response := httptest.NewRecorder()
	adapter.Handler("orders-api", router).ServeHTTP(response, request)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"ok":true`)
	require.Equal(t, traceID, observed)
	select {
	case values := <-seen:
		require.Equal(t, [2]string{"correlation", traceID}, values)
	case <-time.After(time.Second):
		t.Fatal("downstream was not called")
	}
	require.Contains(t, logs.String(), `"trace_id":"correlation"`)
	require.Contains(t, logs.String(), `"otel_trace_id":"`+traceID+`"`)
	require.Contains(t, logs.String(), "request completed")
	require.Eventually(t, func() bool { return len(spans.Ended()) == 3 }, time.Second, time.Millisecond)
	for _, span := range spans.Ended() {
		if span.Name() == "orders-api" {
			attrs := attribute.NewSet(span.Attributes()...)
			route, ok := attrs.Value("http.route")
			require.True(t, ok)
			require.Equal(t, "/orders", route.AsString())
		}
	}
}
