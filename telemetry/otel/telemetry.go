// Package telemetry adapts explicit OpenTelemetry providers to HTTP and slog.
// Applications own SDK/exporter initialization, flushing and shutdown. No global
// providers or propagators are installed. HTTP instrumentation omits full URLs,
// queries, legacy targets and error descriptions; application-added attributes,
// event names, paths and headers still require the application's privacy policy.
package telemetry

import (
	"errors"
	"github.com/maxtanforge/go-frame/internal/isnil"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Options supplies caller-owned providers. TracerProvider is required.
type Options struct {
	TracerProvider trace.TracerProvider
	// MeterProvider defaults to no-op, never the global meter provider.
	MeterProvider metric.MeterProvider
	// Propagator defaults to W3C TraceContext (no baggage).
	Propagator propagation.TextMapPropagator
}

// Adapter is immutable and safe for concurrent use. Construct with New.
type Adapter struct{ options []otelhttp.Option }

// New validates options and assembles the otel HTTP transport adapter.
func New(o Options) (*Adapter, error) {
	if isnil.Check(o.TracerProvider) {
		return nil, errors.New("telemetry: tracer provider is required")
	}
	if o.MeterProvider != nil && isnil.Check(o.MeterProvider) {
		return nil, errors.New("telemetry: typed nil meter provider")
	}
	if o.Propagator != nil && isnil.Check(o.Propagator) {
		return nil, errors.New("telemetry: typed nil propagator")
	}
	if o.MeterProvider == nil {
		o.MeterProvider = noop.NewMeterProvider()
	}
	if o.Propagator == nil {
		o.Propagator = propagation.TraceContext{}
	}
	return &Adapter{options: []otelhttp.Option{otelhttp.WithTracerProvider(safeProvider{TracerProvider: o.TracerProvider}), otelhttp.WithMeterProvider(o.MeterProvider), otelhttp.WithPropagators(o.Propagator)}}, nil
}

// Handler wraps the entire router, outside access logging, so all middleware
// receives the active server span. operation should be a constant service name,
// never a request path. Official instrumentation preserves streaming interfaces.
func (a *Adapter) Handler(operation string, next http.Handler) http.Handler {
	opts := append([]otelhttp.Option{}, a.options...)
	opts = append(opts, otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return operation }))
	return otelhttp.NewHandler(next, operation, opts...)
}

// Transport instruments base. A nil base uses http.DefaultTransport, matching
// otelhttp. Prefer httpclient.Options.WrapTransport for a private owned transport.
// Close or fully consume response bodies to finish client spans.
func (a *Adapter) Transport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base, a.options...)
}
