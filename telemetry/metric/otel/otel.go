// Package metricotel binds a metric.RequestRecorder to OpenTelemetry histograms.
// It is the optional OTLP export path: the meter, SDK and exporters stay owned
// and closed by the caller, exactly like telemetry/otel.
package metricotel

import (
	"context"
	"errors"
	"github.com/maxtanforge/go-frame/internal/isnil"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/maxtanforge/go-frame/telemetry/metric"
)

// NewRequestRecorder builds a recorder whose observations are recorded into an
// otel float64 histogram named http.server.request.duration (seconds) with route
// and method attributes, in addition to the local fixed-bucket histograms.
// opts.OnObserve, when present, keeps running before the otel record.
func NewRequestRecorder(meter otelmetric.Meter, opts metric.Options) (*metric.RequestRecorder, error) {
	if isnil.Check(meter) {
		return nil, errors.New("metricotel: meter must not be nil")
	}
	if _, err := metric.NewRequestRecorder(opts); err != nil {
		return nil, err
	}
	buckets := []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	if opts.Buckets != nil {
		buckets = make([]float64, len(opts.Buckets))
		for i, milliseconds := range opts.Buckets {
			buckets[i] = milliseconds / 1000
		}
	}
	histogram, err := meter.Float64Histogram("http.server.request.duration",
		otelmetric.WithDescription("HTTP server request duration by route template and standard method."),
		otelmetric.WithUnit("s"), otelmetric.WithExplicitBucketBoundaries(buckets...))
	if err != nil {
		return nil, err
	}
	previous := opts.OnObserve
	opts.OnObserve = func(ctx context.Context, o metric.Observation) {
		if previous != nil {
			previous(ctx, o)
		}
		histogram.Record(ctx, o.DurationMS/1000,
			otelmetric.WithAttributes(attribute.String("http.route", o.Route), attribute.String("http.request.method", o.Method)))
	}
	return metric.NewRequestRecorder(opts)
}
