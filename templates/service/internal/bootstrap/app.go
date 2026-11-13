package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/server"
	"github.com/maxtanforge/go-frame/id"
)

func runApplication(ctx context.Context, s settings, lg *slog.Logger) error {
	ids, err := id.NewSnowflake(s.Framework.ID.Node)
	if err != nil {
		return err
	}
	owned, err := newResources(s.Framework)
	if err != nil {
		return err
	}
	checker, err := health.New(health.Options{Checks: owned.checks()})
	if err != nil {
		return err
	}
	address := net.JoinHostPort(s.Host, s.Framework.HTTP.Port)
	services, err := app.NewGroup()
	if err != nil {
		return err
	}
	compose := owned.compositionHook(services, func(_ context.Context, deps dependencies, _ *app.Cleanup) error {
		// Build exactly one complete HTTP router after its dependencies are ready.
		// Authenticated applications use NewAuthenticated at this same boundary.
		routes, err := handler(s, lg, checker, ids)
		if err != nil {
			return err
		}
		if err := composeFeatures(s, routes, deps); err != nil {
			return err
		}
		httpService := server.New(address, routes)
		if err := services.Add(app.NamedService{Name: "http", Service: httpService}); err != nil {
			return errors.Join(err, httpService.Close())
		}
		return nil
	})
	hooks := append(owned.hooks(), compose)
	lifecycle := s.Framework.Lifecycle
	application, err := app.New(services, app.Options{Hooks: hooks, StartupTimeout: lifecycle.StartupTimeout, ShutdownTimeout: lifecycle.ShutdownTimeout, ShutdownDelay: lifecycle.ShutdownDelay, CleanupReserve: lifecycle.CleanupReserve, OnShutdown: func() { checker.SetReady(false) }})
	if err != nil {
		return err
	}
	lg.InfoContext(ctx, "application starting", "address", address)
	return application.Run(ctx)
}
