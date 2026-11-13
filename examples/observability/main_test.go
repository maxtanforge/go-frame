package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/identity"
	"github.com/maxtanforge/go-frame/telemetry/metric"
	"github.com/maxtanforge/go-frame/telemetry/otel"
	"github.com/stretchr/testify/require"
)

func TestShutdownExportsTraceMetricsAndInstance(t *testing.T) {
	var output bytes.Buffer
	meta, err := identity.New(identity.Options{Service: "observability", Environment: "test", InstanceID: "test-instance"})
	require.NoError(t, err)
	adapter, recorder, shutdown, err := newTelemetry(meta, &output)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, shutdown(context.Background())) })
	r := chi.NewRouter()
	r.Use(telemetry.Route)
	r.Use(middleware.AccessLog(nil, middleware.AccessOptions{OnRequest: func(ctx context.Context, e middleware.RequestEvent) {
		require.NoError(t, recorder.Observe(ctx, metric.Observation{Route: e.Route, Method: e.MetricMethod, DurationMS: float64(e.Duration.Microseconds()) / 1000}))
	}}))
	r.Get("/orders/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest("GET", "/orders/42", nil)
	// ParentBased sampling honors an upstream sampled parent deterministically.
	req.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	adapter.Handler("observability", r).ServeHTTP(httptest.NewRecorder(), req)
	require.NoError(t, shutdown(context.Background()))
	require.Contains(t, output.String(), "test-instance")
	require.Contains(t, output.String(), "http.route")
	// 双写链路:本地直方图也收到了同一次请求的观测。
	require.Equal(t, uint64(1), recorder.Snapshot()[metric.RequestKey{Route: "/orders/{id}", Method: "GET"}].Count)
	require.Contains(t, output.String(), "/orders/{id}")
	require.Contains(t, output.String(), "ScopeMetrics")
	require.Contains(t, output.String(), "SpanContext")
}
