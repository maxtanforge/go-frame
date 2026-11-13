// Package middleware provides request logging, cooperative deadlines and bounded admission.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/internal/requestmeta"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

// RequestEvent is a completed request observation. Route is a template rather
// than the raw path, avoiding unbounded metric labels. Path is for logs only.
// Status zero means no response was committed before cancellation or abort.
type RequestEvent struct {
	Method       string // Original HTTP method; use MetricMethod for metric labels.
	MetricMethod string // Standard method or _OTHER, a bounded label set.
	Route        string
	Path         string // Encoded request path without query; never a metric label.
	Status       int
	Bytes        int64
	Duration     time.Duration
	Canceled     bool
	Aborted      bool
}

// AccessOptions controls completion logging and an optional metrics hook.
type AccessOptions struct {
	OnRequest   func(context.Context, RequestEvent)
	QuietHealth bool
}

// AccessLog injects the request logger and records one completion event. The
// callback must be fast and concurrency-safe; its panic is logged and isolated.
func AccessLog(base *slog.Logger, opts AccessOptions) func(http.Handler) http.Handler {
	if base == nil {
		base = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(requestmeta.Ensure(r.Context()))
			l := base.With("trace_id", trace.FromContext(r.Context()))
			r = r.WithContext(logger.IntoContext(r.Context(), l))
			start := time.Now()
			path := requestmeta.Path(r)
			rw := &accessWriter{ResponseWriter: w}
			defer func() {
				finished := time.Now()
				if at, ok := requestmeta.Finished(r.Context()); ok {
					finished = at
				}
				failure := recover()
				if failure != nil {
					defer func() { panic(failure) }()
				}
				route := "<unmatched>"
				quietPath := r.URL.Path
				if rc := chi.RouteContext(r.Context()); rc != nil {
					if rc.RoutePattern() != "" {
						route = rc.RoutePattern()
					}
					// 挂载在子路由前缀下时按路由相对路径匹配，QuietHealth 不因挂载而失效。
					if rc.RoutePath != "" {
						quietPath = rc.RoutePath
					}
				}
				status := rw.Status()
				canceled := r.Context().Err() == context.Canceled
				if status == 0 && !canceled && failure == nil && !rw.Hijacked() {
					status = http.StatusOK
				}
				event := RequestEvent{Method: r.Method, MetricMethod: MetricMethod(r.Method), Route: route, Path: path, Status: status, Bytes: rw.BytesWritten(), Duration: finished.Sub(start), Canceled: canceled, Aborted: failure != nil}
				completionCtx := r.Context()
				if actor, ok := requestmeta.Actor(completionCtx); ok {
					l = l.With("actor_id", actor.ID, "actor_name", actor.Name, "actor_issuer", actor.Issuer)
					completionCtx = principal.IntoContext(completionCtx, actor)
				}
				if !(opts.QuietHealth && status >= 200 && status < 400 && (quietPath == "/healthz" || quietPath == "/readyz")) {
					level := slog.LevelInfo
					if status >= 500 || event.Aborted {
						level = slog.LevelError
					}
					l.Log(completionCtx, level, "request completed", "kind", "access", "method", event.Method, "path", event.Path, "route", event.Route, "status", event.Status, "bytes", event.Bytes, "duration_ms", float64(event.Duration)/float64(time.Millisecond), "canceled", event.Canceled, "aborted", event.Aborted)
				}
				if opts.OnRequest != nil {
					observe(completionCtx, l, opts.OnRequest, event)
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}

func observe(ctx context.Context, l *slog.Logger, f func(context.Context, RequestEvent), event RequestEvent) {
	defer func() {
		if p := recover(); p != nil {
			l.ErrorContext(ctx, "request observer panic", "panic", p)
		}
	}()
	f(ctx, event)
}

// Deadline sets a cooperative request deadline without racing a second writer.
// respond.Adapter maps expiry to 504 before committing its response. Native
// handlers own their response and must check ctx.Err() before writing. This
// does not forcibly terminate handlers and does not buffer streaming responses.
func Deadline(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if timeout <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
