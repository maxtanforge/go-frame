package telemetry

import (
	"context"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
)

// LogHandler adds otel_trace_id and otel_span_id for valid span contexts,
// independently of the application's trace_id. Attributes follow the wrapped
// handler's current group. Configure it before deriving loggers with WithGroup.
func LogHandler(next slog.Handler) slog.Handler { return logHandler{next: next} }

type logHandler struct{ next slog.Handler }

func (h logHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }
func (h logHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(slog.String("otel_trace_id", sc.TraceID().String()), slog.String("otel_span_id", sc.SpanID().String()))
	}
	return h.next.Handle(ctx, r)
}
func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{next: h.next.WithAttrs(attrs)}
}
func (h logHandler) WithGroup(name string) slog.Handler {
	return logHandler{next: h.next.WithGroup(name)}
}
