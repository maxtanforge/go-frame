package bootstrap

import (
	"io"
	"log/slog"
	"net/http"

	"example.com/service/internal/greeting"
	greetinghttp "example.com/service/internal/greeting/http"
	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/telemetry/metric"
	metricredis "github.com/maxtanforge/go-frame/telemetry/metric/redis"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

func handler(s settings, lg *slog.Logger, checker *health.Checker, ids *id.Snowflake) (chi.Router, error) {
	resolver, err := request.NewClientIPResolver(s.TrustedProxies)
	if err != nil {
		return nil, err
	}
	recorder, err := operationhttp.New(operation.NewSlogSink(lg), operationhttp.Options{Request: operationhttp.RequestOptions{
		Query: operationhttp.Disabled, Params: operationhttp.Disabled, Body: operationhttp.Disabled,
	}})
	if err != nil {
		return nil, err
	}
	return router.New(lg, router.Options{Timeout: s.Framework.HTTP.Timeout, Health: checker, QuietHealth: true, Trace: []trace.Option{trace.WithSnowflake(ids)}, Middleware: []func(http.Handler) http.Handler{resolver.Middleware, recorder.Middleware}}), nil
}

func composeFeatures(s settings, routes chi.Router, deps dependencies) error {
	if s.MetricsEnabled {
		var pools []metric.PoolSource
		if deps.Database != nil {
			pools = append(pools, metric.SQLPool("database", deps.Database.SQLDB()))
		}
		if deps.Cache != nil {
			pools = append(pools, metricredis.Pool("cache", deps.Cache.Client))
		}
		resources, err := metric.NewResourceRecorder(pools...)
		if err != nil {
			return err
		}
		routes.Get("/metrics", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, _ = io.WriteString(w, resources.Prometheus())
		})
	}
	// Greeting requires no infrastructure. New features receive their concrete
	// dependencies here; see README for database/cache injection.
	service := greeting.Service{Prefix: s.Greeting.Prefix}
	declarations, err := greetinghttp.Routes(service)
	if err != nil {
		return err
	}
	for _, declaration := range declarations {
		routes.With(operationhttp.With(declaration.Operation)).Method(declaration.Method, declaration.Path, declaration.Handler)
	}
	return nil
}
