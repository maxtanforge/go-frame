package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/metric"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

// newRoutes supplies the common HTTP boundary. Register keeps ownership of CRUD
// handlers; any business endpoint added to this router requires authentication.
func newRoutes(lg *slog.Logger, opts router.Options, verifier auth.Verifier, sink operation.Sink, metrics *metric.RequestRecorder) (*router.Authenticated, error) {
	if lg == nil {
		lg = slog.Default()
	}
	if metrics == nil {
		return nil, errors.New("ent example: metrics recorder is required")
	}
	recorder, err := operationhttp.New(sink, operationhttp.Options{})
	if err != nil {
		return nil, err
	}
	opts.OnRequest = func(ctx context.Context, event middleware.RequestEvent) {
		if err := metrics.Observe(ctx, metric.Observation{Route: event.Route, Method: event.MetricMethod, DurationMS: float64(event.Duration) / float64(time.Millisecond)}); err != nil {
			lg.ErrorContext(ctx, "request metric failed", "error", err)
		}
	}
	return router.NewAuthenticated(lg, router.AuthenticatedOptions{
		Router: opts, Verifier: verifier, Operations: recorder,
		Routes: []router.Route{{Method: "GET", Path: "/metrics", Handler: respond.Adapter(func(w http.ResponseWriter, _ *http.Request) (any, error) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, err := io.WriteString(w, metrics.Prometheus())
			return nil, err
		})}},
		PublicEndpoints: []auth.Endpoint{
			{Method: "GET", Path: "/healthz"}, {Method: "HEAD", Path: "/healthz"},
			{Method: "GET", Path: "/readyz"}, {Method: "HEAD", Path: "/readyz"},
		},
	})
}
