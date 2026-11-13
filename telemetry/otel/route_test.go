package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestChiRouteLabelsSpansAndMetrics(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		require.NoError(t, tp.Shutdown(context.Background()))
		require.NoError(t, mp.Shutdown(context.Background()))
	})
	a, err := New(Options{TracerProvider: tp, MeterProvider: mp})
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Use(Route)
	r.Route("/api", func(r chi.Router) {
		r.Get("/orders/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		r.Get("/items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	})
	h := a.Handler("api", r)
	for _, path := range []string{"/api/orders/secret-one", "/api/orders/secret-two", "/api/items/secret-three"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path+"?token=secret", nil))
	}
	want := []string{"/api/orders/{id}", "/api/orders/{id}", "/api/items/{id}"}
	require.Len(t, spans.Ended(), len(want))
	for i, span := range spans.Ended() {
		attrs := attribute.NewSet(span.Attributes()...)
		route, ok := attrs.Value("http.route")
		assert.True(t, ok, "span is missing route")
		assert.Equal(t, want[i], route.AsString())
	}
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))
	found := false
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if hist, ok := metric.Data.(metricdata.Histogram[float64]); ok {
				found = true
				counts := map[string]uint64{}
				for _, point := range hist.DataPoints {
					route, ok := point.Attributes.Value("http.route")
					require.True(t, ok, "metric is missing route")
					counts[route.AsString()] += point.Count
					for _, a := range point.Attributes.ToSlice() {
						require.NotContains(t, a.Value.String(), "secret")
					}
				}
				require.Equal(t, map[string]uint64{"/api/orders/{id}": 2, "/api/items/{id}": 1}, counts)
			}
		}
	}
	require.True(t, found)
}

func TestRouteUnmatchedAndAbortPrivacy(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() { require.NoError(t, tp.Shutdown(context.Background())) })
	a, err := New(Options{TracerProvider: tp})
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Use(Route)
	r.Route("/api", func(r chi.Router) {
		r.Get("/abort/{id}", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	})
	h := a.Handler("api", r)
	for _, path := range []string{"/secret", "/api/secret"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		require.Equal(t, http.StatusNotFound, response.Code)
	}
	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/abort/secret", nil))
	})
	want := []string{"", "/api/*", "/api/abort/{id}"}
	require.Len(t, spans.Ended(), len(want))
	for i, span := range spans.Ended() {
		attrs := attribute.NewSet(span.Attributes()...)
		route, _ := attrs.Value("http.route")
		require.Equal(t, want[i], route.AsString())
		for _, attr := range span.Attributes() {
			if attr.Key == "http.route" {
				require.NotContains(t, attr.Value.String(), "secret")
			}
		}
	}
	// Using the middleware without chi or providers is harmless.
	Route(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/secret", nil))
}
