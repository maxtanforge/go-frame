// Package router composes chi routes with framework middleware and health endpoints.
package router

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/health"
	healthhttp "github.com/maxtanforge/go-frame/http/health"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

// Options describes HTTP composition; register business routes on the
// returned chi.Router before serving. Authentication belongs to route groups.
type Options struct {
	// Timeout defaults to 30s. Negative disables it (e.g. streaming servers).
	// Cancellation is cooperative: handlers must honor their context.
	Timeout    time.Duration
	Health     *health.Checker
	Trace      []trace.Option
	Middleware []func(http.Handler) http.Handler
	// DisableHealthMount skips mounting /healthz and /readyz when external
	// probing owns liveness. Mounting stays the default; a missing checker
	// still fails closed on both endpoints.
	DisableHealthMount bool
	// OnRequest is a synchronous, concurrency-safe completion observer for metrics.
	OnRequest func(context.Context, middleware.RequestEvent)
	// QuietHealth suppresses successful health endpoint access logs, not metrics.
	QuietHealth bool
}

// New provides trace, access logging, recovery, deadlines and health routes.
// It trusts no forwarded IP headers. Register features directly or with Route/Group.
// Use one framework router per request path. NewAuthenticated already includes
// this boundary; use a plain chi parent if a separate mount prefix is needed.
func New(lg *slog.Logger, opts Options) chi.Router {
	if lg == nil {
		lg = slog.Default()
	}
	r := chi.NewRouter()
	// Seed the configured logger before recovery and trace initialization: early
	// panics must reach the same sink even when AccessLog has not run yet.
	// Keep inner recovery below deadlines to retain the deadline-bearing request.
	r.Use(func(next http.Handler) http.Handler {
		recovered := respond.Recoverer(next)
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			recovered.ServeHTTP(w, req.WithContext(logger.IntoContext(req.Context(), lg)))
		})
	})
	r.Use(trace.Middleware(opts.Trace...))
	r.Use(middleware.AccessLog(lg, middleware.AccessOptions{OnRequest: opts.OnRequest, QuietHealth: opts.QuietHealth}))
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Timeout > 0 {
		r.Use(middleware.Deadline(opts.Timeout))
	}
	// Recovery must see the deadline-bearing request, including native handlers.
	r.Use(respond.Recoverer)
	for _, m := range opts.Middleware {
		if m != nil {
			r.Use(m)
		}
	}
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		respond.WriteError(w, r, &httperror.Error{Status: 404, Message: "not found"})
	})
	var methodOnce sync.Once
	var methods []string
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		// Routes are immutable once serving starts. Cache method names, not paths.
		methodOnce.Do(func() {
			set := map[string]bool{}
			collectMethods(r, set)
			for method := range set {
				methods = append(methods, method)
			}
			sort.Strings(methods)
		})
		var routes chi.Routes = r
		if rc := chi.RouteContext(req.Context()); rc != nil && rc.Routes != nil {
			routes = rc.Routes
		}
		path := req.URL.RawPath
		if path == "" {
			path = req.URL.Path
		}
		if rc := chi.RouteContext(req.Context()); rc != nil && rc.RoutePath != "" {
			path = rc.RoutePath
			// RoutePath is relative to the current subrouter, but Routes points
			// at the root. Follow the matched mount patterns before probing it.
			for _, pattern := range rc.RoutePatterns {
				var child chi.Routes
				for _, route := range routes.Routes() {
					if route.SubRoutes != nil && (route.Pattern == pattern || strings.TrimSuffix(route.Pattern, "/*") == strings.TrimSuffix(pattern, "/")) {
						child = route.SubRoutes
						break
					}
				}
				if child == nil {
					break
				}
				routes = child
			}
		}
		if path == "" {
			path = "/"
		}
		var allowed []string
		for _, method := range methods {
			if routes.Match(chi.NewRouteContext(), method, path) {
				allowed = append(allowed, method)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}
		respond.WriteError(w, req, &httperror.Error{Status: 405, Message: "method not allowed"})
	})
	// A missing checker is a configuration error; healthhttp fails closed
	// (503 on both endpoints) rather than reporting a fabricated ready state.
	if !opts.DisableHealthMount {
		for path, healthHandler := range map[string]http.Handler{
			"/healthz": healthhttp.NewLiveness(opts.Health),
			"/readyz":  healthhttp.NewReadiness(opts.Health),
		} {
			r.Method(http.MethodGet, path, healthHandler)
			r.Method(http.MethodHead, path, healthHandler)
		}
	}
	return r
}

func collectMethods(routes chi.Routes, set map[string]bool) {
	for _, route := range routes.Routes() {
		for method := range route.Handlers {
			if method != "*" {
				set[method] = true
			}
		}
		if route.SubRoutes != nil {
			collectMethods(route.SubRoutes, set)
		}
	}
}
