package toolkit

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

// NewHTTP assembles transport middleware directly on chi instead of
// router.New: trace correlation first, then access logging with an event
// hook, then a cooperative deadline for the plain API group only. Response
// entry points come from the respond capability package.
func NewHTTP(lg *slog.Logger, onRequest func(context.Context, middleware.RequestEvent)) http.Handler {
	r := chi.NewRouter()
	r.Use(trace.Middleware())
	r.Use(middleware.AccessLog(lg, middleware.AccessOptions{OnRequest: onRequest}))
	r.Group(func(g chi.Router) {
		g.Use(middleware.Deadline(10 * time.Second))
		g.Get("/status", respond.Adapter(func(_ http.ResponseWriter, r *http.Request) (any, error) {
			return map[string]string{"trace": trace.FromContext(r.Context())}, nil
		}))
	})
	return r
}
