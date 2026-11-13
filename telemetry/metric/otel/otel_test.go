package metricotel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/maxtanforge/go-frame/telemetry/metric"
)

func TestBindRecordsIntoOTelHistogram(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	buckets := []float64{100}
	recorder, err := NewRequestRecorder(provider.Meter("frame-test"), metric.Options{Buckets: buckets})
	require.NoError(t, err)
	buckets[0] = 200
	if err := recorder.Observe(context.Background(), metric.Observation{Route: "/orders", Method: "GET", DurationMS: 42}); err != nil {
		t.Error(err)
		return
	}
	if err := recorder.Observe(context.Background(), metric.Observation{Route: "/orders", Method: "GET", DurationMS: 250}); err != nil {
		t.Error(err)
		return
	}
	local := recorder.Snapshot()[metric.RequestKey{Route: "/orders", Method: "GET"}]
	require.Equal(t, uint64(2), local.Count)
	require.Equal(t, 100.0, local.Buckets[0].UpperBound)

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &collected))
	require.Len(t, collected.ScopeMetrics, 1)
	found := false
	for _, metricFamily := range collected.ScopeMetrics[0].Metrics {
		if metricFamily.Name != "http.server.request.duration" {
			continue
		}
		require.Equal(t, "s", metricFamily.Unit)
		histogram, ok := metricFamily.Data.(metricdata.Histogram[float64])
		require.True(t, ok)
		require.Len(t, histogram.DataPoints, 1)
		point := histogram.DataPoints[0]
		require.Equal(t, uint64(2), point.Count)
		require.Equal(t, []float64{0.1}, point.Bounds)
		require.Equal(t, []uint64{1, 1}, point.BucketCounts)
		require.InDelta(t, 0.292, point.Sum, 1e-9)
		method, ok := point.Attributes.Value("http.request.method")
		require.True(t, ok)
		require.Equal(t, "GET", method.AsString())
		route, ok := point.Attributes.Value("http.route")
		require.True(t, ok)
		require.Equal(t, "/orders", route.AsString())
		found = true
	}
	require.True(t, found, "otel 直方图必须收到双写观测")
}

func TestRejectsMissingMeterAndInvalidConfiguration(t *testing.T) {
	recorder, err := NewRequestRecorder(nil, metric.Options{})
	require.Error(t, err)
	require.Nil(t, recorder)
	provider := sdkmetric.NewMeterProvider()
	defer func() { require.NoError(t, provider.Shutdown(context.Background())) }()
	recorder, err = NewRequestRecorder(provider.Meter("invalid"), metric.Options{Buckets: []float64{2, 1}})
	require.Error(t, err)
	require.Nil(t, recorder)
}
