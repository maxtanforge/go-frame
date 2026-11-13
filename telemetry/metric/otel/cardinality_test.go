package metricotel

import (
	"context"
	"strconv"
	"testing"

	"github.com/maxtanforge/go-frame/telemetry/metric"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestOTelUsesTheSameBoundedKeysAsLocalRecorder(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	recorder, err := NewRequestRecorder(provider.Meter("bounded-test"), metric.Options{})
	require.NoError(t, err)
	for i := 0; i < 600; i++ {
		if err := recorder.Observe(context.Background(), metric.Observation{Route: "/routes/" + strconv.Itoa(i), Method: "GET", DurationMS: 1}); err != nil {
			t.Error(err)
			return
		}
	}
	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &collected))
	local := recorder.Snapshot()
	points := 0
	for _, scope := range collected.ScopeMetrics {
		for _, family := range scope.Metrics {
			histogram, ok := family.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range histogram.DataPoints {
				points++
				route, _ := point.Attributes.Value("http.route")
				method, _ := point.Attributes.Value("http.request.method")
				key := metric.RequestKey{Route: route.AsString(), Method: method.AsString()}
				value, exists := local[key]
				require.True(t, exists, "exported key %v is absent locally", key)
				require.Equal(t, value.Count, point.Count)
			}
		}
	}
	require.Equal(t, len(local), points)
}
