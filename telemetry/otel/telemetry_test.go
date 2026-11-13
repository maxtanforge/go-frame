package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	apitrace "go.opentelemetry.io/otel/trace"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPFailurePrivacyAndStreaming(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	a, _ := New(Options{TracerProvider: provider})
	req := httptest.NewRequest("GET", "http://user:secret@example.test/?token=secret", nil)
	failure := errors.New("request http://user:secret@example.test/?token=secret failed")
	_, err := a.Transport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != req.URL.String() {
			t.Error("URL changed")
		}
		return nil, failure
	})).RoundTrip(req)
	if !errors.Is(err, failure) {
		t.Fatal("error identity changed")
	}
	s := recorder.Ended()[0]
	if s.Status().Code != codes.Error || strings.Contains(s.Status().Description, "secret") {
		t.Fatal(s.Status())
	}
	a.Handler("stream", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
	})).ServeHTTP(httptest.NewRecorder(), req)
}

func TestExplicitMeterAndLowCardinality(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = meter.Shutdown(context.Background()) }()
	provider := trace.NewTracerProvider()
	defer func() { _ = provider.Shutdown(context.Background()) }()
	a, _ := New(Options{TracerProvider: provider, MeterProvider: meter, Propagator: propagation.TraceContext{}})
	h := a.Handler("api", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://example.test/users/123?token=secret", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://example.test/users/456?token=secret", nil))
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch v := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, p := range v.DataPoints {
					count++
					if p.Count != 2 {
						t.Fatalf("cardinality split: %v", p)
					}
					for _, attr := range p.Attributes.ToSlice() {
						if strings.Contains(attr.Value.String(), "/users/") || strings.Contains(attr.Value.String(), "secret") {
							t.Fatal(attr)
						}
					}
				}
			}
		}
	}
	if count == 0 {
		t.Fatal("no HTTP histogram")
	}
	var nilMeter *sdkmetric.MeterProvider
	if _, err := New(Options{TracerProvider: provider, MeterProvider: nilMeter}); err == nil {
		t.Fatal("typed nil meter accepted")
	}
}

func TestDefaultsIgnoreGlobalMeterAndPropagator(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = meter.Shutdown(context.Background()) }()
	oldMeter, oldPropagator := otel.GetMeterProvider(), otel.GetTextMapPropagator()
	otel.SetMeterProvider(meter)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	defer otel.SetMeterProvider(oldMeter)
	defer otel.SetTextMapPropagator(oldPropagator)
	provider := trace.NewTracerProvider()
	defer func() { _ = provider.Shutdown(context.Background()) }()
	a, err := New(Options{TracerProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	h := a.Handler("api", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := apitrace.SpanContextFromContext(r.Context()).TraceID().String(); got != "0123456789abcdef0123456789abcdef" {
			t.Fatalf("global propagator used: %s", got)
		}
	}))
	req := httptest.NewRequest("GET", "http://example.test/", nil)
	req.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	h.ServeHTTP(httptest.NewRecorder(), req)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.ScopeMetrics) != 0 {
		t.Fatal("default adapter used global meter")
	}
	if otel.GetMeterProvider() != meter {
		t.Fatal("adapter replaced global meter")
	}
}

func TestExplicitProviderAndPropagation(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing provider accepted")
	}
	var nilProvider *trace.TracerProvider
	if _, err := New(Options{TracerProvider: nilProvider}); err == nil {
		t.Fatal("typed nil accepted")
	}
	global := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	a, err := New(Options{TracerProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	var propagated string
	downstream := httptest.NewServer(a.Handler("downstream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		propagated = r.Header.Get("traceparent")
		if r.URL.RawQuery != "token=secret" {
			t.Error("query changed")
		}
		w.WriteHeader(204)
	})))
	defer downstream.Close()
	client := &http.Client{Transport: a.Transport(http.DefaultTransport.(*http.Transport).Clone())}
	defer client.CloseIdleConnections()
	var logs bytes.Buffer
	logger := slog.New(LogHandler(slog.NewJSONHandler(&logs, nil))).With("trace_id", "correlation").WithGroup("request").With("service", "test")
	upstream := a.Handler("upstream", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "inside")
		req, _ := http.NewRequestWithContext(r.Context(), "GET", downstream.URL+"/?token=secret", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}))
	req := httptest.NewRequest("GET", "http://user:password@example.test/?token=secret", nil)
	req.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	upstream.ServeHTTP(httptest.NewRecorder(), req)
	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("spans=%d", len(spans))
	}
	var inbound, outbound, final trace.ReadOnlySpan
	for _, s := range spans {
		switch s.Name() {
		case "upstream":
			inbound = s
		case "downstream":
			final = s
		default:
			outbound = s
		}
		for _, attr := range s.Attributes() {
			if strings.Contains(attr.Value.String(), "secret") || strings.Contains(attr.Value.String(), "password") {
				t.Fatalf("sensitive attr %v", attr)
			}
		}
	}
	if inbound.Parent().SpanID().String() != "0123456789abcdef" || outbound.Parent().SpanID() != inbound.SpanContext().SpanID() || final.Parent().SpanID() != outbound.SpanContext().SpanID() || propagated == "" {
		t.Fatal("broken parent chain")
	}
	if !strings.Contains(logs.String(), `"trace_id":"correlation"`) || !strings.Contains(logs.String(), `"otel_trace_id":"0123456789abcdef0123456789abcdef"`) || !strings.Contains(logs.String(), `"service":"test"`) {
		t.Fatal(logs.String())
	}
	if otel.GetTracerProvider() != global {
		t.Fatal("global provider changed")
	}
}

func TestLogHandlerDynamicLevelAndNoSpan(t *testing.T) {
	var logs bytes.Buffer
	var level slog.LevelVar
	logger := slog.New(LogHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: &level})))
	logger.Debug("hidden")
	level.Set(slog.LevelDebug)
	logger.Debug("visible")
	if strings.Contains(logs.String(), "hidden") || !strings.Contains(logs.String(), "visible") || strings.Contains(logs.String(), "otel_trace_id") {
		t.Fatal(logs.String())
	}
}

func TestInstrumentationPrivacy(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	p := safeProvider{TracerProvider: provider}
	_, span := p.Tracer("test").Start(context.Background(), "http", apitrace.WithAttributes(attribute.String("url.full", "http://user:secret@example.test/?token=secret")))
	span.SetAttributes(attribute.String("url.query", "secret"), attribute.String("http.target", "/?token=secret"))
	span.SetStatus(codes.Error, "secret")
	span.RecordError(errors.New("secret"))
	span.AddEvent("event", apitrace.WithAttributes(attribute.String("exception.message", "secret")))
	span.End()
	s := recorder.Ended()[0]
	if s.Status().Description != "" {
		t.Fatal("status leaked")
	}
	for _, a := range s.Attributes() {
		if strings.Contains(a.Value.String(), "secret") {
			t.Fatal(a)
		}
	}
	for _, e := range s.Events() {
		for _, a := range e.Attributes {
			if strings.Contains(a.Value.String(), "secret") {
				t.Fatal(a)
			}
		}
	}
}
