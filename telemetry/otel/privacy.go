package telemetry

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// This narrowly filters known HTTP URL and error fields emitted by otelhttp.
// It is not a general redaction boundary for arbitrary application telemetry.
type safeProvider struct{ trace.TracerProvider }

func (p safeProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return safeTracer{Tracer: p.TracerProvider.Tracer(name, opts...)}
}

type safeTracer struct{ trace.Tracer }

func (t safeTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	// Rebuild options so disallowed initial attributes never reach the SDK.
	clean := []trace.SpanStartOption{trace.WithAttributes(safeAttributes(cfg.Attributes())...), trace.WithLinks(cfg.Links()...), trace.WithTimestamp(cfg.Timestamp()), trace.WithSpanKind(cfg.SpanKind())}
	if cfg.NewRoot() {
		clean = append(clean, trace.WithNewRoot())
	}
	ctx, span := t.Tracer.Start(ctx, name, clean...)
	wrapped := safeSpan{Span: span}
	return trace.ContextWithSpan(ctx, wrapped), wrapped
}

type safeSpan struct{ trace.Span }

func (s safeSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.Span.SetAttributes(safeAttributes(attrs)...)
}
func (s safeSpan) SetStatus(code codes.Code, _ string) { s.Span.SetStatus(code, "") }
func (s safeSpan) RecordError(err error, opts ...trace.EventOption) {
	if err != nil {
		s.Span.RecordError(errors.New("HTTP operation failed"), safeEventOptions(opts)...)
	}
}
func (s safeSpan) AddEvent(name string, opts ...trace.EventOption) {
	s.Span.AddEvent(name, safeEventOptions(opts)...)
}
func safeEventOptions(opts []trace.EventOption) []trace.EventOption {
	cfg := trace.NewEventConfig(opts...)
	return []trace.EventOption{trace.WithTimestamp(cfg.Timestamp()), trace.WithAttributes(safeAttributes(cfg.Attributes())...)}
}
func safeAttributes(attrs []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		switch string(a.Key) {
		case "url.full", "url.query", "http.url", "http.target", "exception.message", "exception.stacktrace", "error.message":
			continue
		}
		out = append(out, a)
	}
	return out
}
