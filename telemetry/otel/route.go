package telemetry

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Route forwards chi's matched route template to the active server span and
// otelhttp metrics. Install once using chi.Use or router.Options.Middleware,
// inside Adapter.Handler. Templates are resolved after routing, including nested
// routers; raw paths and query values are never used. Requests without a matched
// template receive no route attribute. Partially matched mounts retain their
// bounded wildcard template. Panic/ErrAbortHandler propagation is unchanged.
func Route(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rc := chi.RouteContext(r.Context())
			if rc == nil {
				return
			}
			pattern := rc.RoutePattern()
			if pattern == "" {
				return
			}
			label := attribute.String("http.route", pattern)
			trace.SpanFromContext(r.Context()).SetAttributes(label)
			if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
				labeler.Add(label)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
