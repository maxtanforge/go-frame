// The example starts without a database or Redis. Set DATABASE_URL / REDIS_ADDR to
// attach dependencies; startup probes and shutdown cleanup then run automatically.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/maxtanforge/go-frame/app"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	redisconfig "github.com/maxtanforge/go-frame/app/config/redis"
	"github.com/maxtanforge/go-frame/app/resource"
	"github.com/maxtanforge/go-frame/app/startup"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/database/mysql"
	"github.com/maxtanforge/go-frame/database/postgres"
	"github.com/maxtanforge/go-frame/examples/basic/internal/greeting"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/http/server"
	"github.com/maxtanforge/go-frame/redis"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	return execute(ctx, appconfig.Load, runApplication)
}

// Keep logging alive until the app has drained and all resource hooks stopped.
// Do not mutate slog.Default: multiple embedded instances must remain isolated.
func runWithConfig(ctx context.Context, cfg appconfig.Config) error {
	return execute(ctx, func() (appconfig.Config, error) { return cfg, nil }, runApplication)
}

func execute(ctx context.Context, load func() (appconfig.Config, error), run func(context.Context, appconfig.Config, *slog.Logger) error) error {
	return startup.Run(ctx, startup.RunOptions[appconfig.Config]{Load: func() (appconfig.Config, error) {
		cfg, err := load()
		if err != nil {
			return cfg, err
		}
		return cfg, cfg.Validate()
	}, Logging: func(cfg appconfig.Config) startup.LogSettings {
		return startup.LogSettings{Service: cfg.Service, Log: cfg.Log}
	}, Run: run})
}
func runApplication(ctx context.Context, cfg appconfig.Config, lg *slog.Logger) error {
	var hooks []app.Hook
	var checks []health.Check
	if cfg.Database.URL != "" {
		db, err := resource.New(resource.Options[*database.DB]{
			Name: "database",
			Open: func(context.Context) (*database.DB, error) {
				return openDatabase(cfg.Database)
			},
			Probe: func(ctx context.Context, db *database.DB) error { return db.Ping(ctx) },
			Close: func(_ context.Context, db *database.DB) error { return db.Close() },
		})
		if err != nil {
			return err
		}
		hooks = append(hooks, db.Hook())
		checks = append(checks, health.Check{Name: db.Name(), Probe: db.Probe})
	}
	if cfg.Redis.Addr != "" {
		cache, err := resource.New(resource.Options[*redis.Pool]{
			Name: "redis",
			Open: func(context.Context) (*redis.Pool, error) {
				opts, err := redisconfig.Options(cfg.Redis)
				if err != nil {
					return nil, err
				}
				return redis.Open(opts)
			},
			Probe: func(ctx context.Context, cache *redis.Pool) error { return cache.Ping(ctx) },
			Close: func(_ context.Context, cache *redis.Pool) error { return cache.Close() },
		})
		if err != nil {
			return err
		}
		hooks = append(hooks, cache.Hook())
		checks = append(checks, health.Check{Name: cache.Name(), Probe: cache.Probe})
	}
	checker, err := health.New(health.Options{Checks: checks})
	if err != nil {
		return err
	}
	routes := router.New(lg, router.Options{Timeout: cfg.HTTP.Timeout, Health: checker, QuietHealth: true})
	greeting.Register(routes, greeting.Service{Prefix: "Hello"})
	service := server.New(":"+cfg.HTTP.Port, routes)
	application, err := app.New(service, app.Options{Hooks: hooks, StartupTimeout: cfg.Lifecycle.StartupTimeout, CleanupReserve: cfg.Lifecycle.CleanupReserve, ShutdownTimeout: cfg.Lifecycle.ShutdownTimeout, ShutdownDelay: cfg.Lifecycle.ShutdownDelay, OnShutdown: func() { checker.SetReady(false) }})
	if err != nil {
		return err
	}
	lg.InfoContext(ctx, "application starting", "port", cfg.HTTP.Port)
	return application.Run(ctx)
}

// The composition root intentionally supports both drivers. A service using one
// database can import only its adapter and omit this runtime selection.
func openDatabase(cfg appconfig.DatabaseConfig) (*database.DB, error) {
	pool := database.Options{MaxOpenConns: cfg.MaxOpenConns, MaxIdleConns: cfg.MaxIdleConns, ConnMaxIdleTime: cfg.ConnMaxIdleTime, ConnMaxLifetime: cfg.ConnMaxLifetime}
	switch cfg.Driver {
	case "mysql":
		return mysql.Open(mysql.Options{DSN: cfg.URL, Pool: pool})
	case "postgres":
		return postgres.Open(postgres.Options{DSN: cfg.URL, Pool: pool})
	default:
		return nil, errors.New("unsupported database driver")
	}
}
