package bootstrap

import (
	"log/slog"
	"time"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	categoryhttp "github.com/maxtanforge/go-frame/examples/business/internal/category/http"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

func newRouter(catalog *category.Catalog, verifier auth.Verifier, sink operation.Sink, checker *health.Checker, lg *slog.Logger, timeout time.Duration, ids *id.Snowflake) (*router.Authenticated, error) {
	declarations, err := categoryhttp.CRUDRoutes(catalog)
	if err != nil {
		return nil, err
	}
	recorder, err := operationhttp.New(sink, operationhttp.Options{})
	if err != nil {
		return nil, err
	}
	return router.NewAuthenticated(lg, router.AuthenticatedOptions{
		Router: router.Options{Timeout: timeout, Health: checker, QuietHealth: true, Trace: []trace.Option{trace.WithSnowflake(ids)}}, Verifier: verifier, Operations: recorder, Routes: declarations,
		PublicEndpoints: []auth.Endpoint{{Method: "GET", Path: "/healthz"}, {Method: "HEAD", Path: "/healthz"}, {Method: "GET", Path: "/readyz"}, {Method: "HEAD", Path: "/readyz"}},
	})
}
