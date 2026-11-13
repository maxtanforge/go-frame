package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/maxtanforge/go-frame/app"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/app/resource"
	"github.com/maxtanforge/go-frame/app/startup"
	"github.com/maxtanforge/go-frame/database"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/database/mysql"
	"github.com/maxtanforge/go-frame/database/postgres"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/examples/business/internal/category/store"
	"github.com/maxtanforge/go-frame/examples/business/internal/ent"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/server"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/telemetry/operation"
)

// Run loads settings and runs the service until cancellation or termination.
func Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("business: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return startup.Run(ctx, startup.RunOptions[Settings]{
		Load: func() (Settings, error) { return LoadFrom(os.LookupEnv) },
		Logging: func(s Settings) startup.LogSettings {
			return startup.LogSettings{Service: s.Config.Service, Log: s.Config.Log}
		},
		Run: runApplication,
	})
}

func runApplication(ctx context.Context, s Settings, lg *slog.Logger) error {
	if err := validateSettings(s); err != nil {
		return err
	}
	ids, err := id.NewSnowflake(s.Config.ID.Node)
	if err != nil {
		return err
	}
	pool, err := resource.New(resource.Options[*database.DB]{Name: "database",
		Open:  func(context.Context) (*database.DB, error) { return openDatabase(s.Config.Database) },
		Probe: func(ctx context.Context, db *database.DB) error { return db.Ping(ctx) },
		Close: func(_ context.Context, db *database.DB) error { return db.Close() },
	})
	if err != nil {
		return err
	}
	checker, err := health.New(health.Options{Checks: []health.Check{{Name: pool.Name(), Probe: pool.Probe}}})
	if err != nil {
		return err
	}
	services, err := app.NewGroup()
	if err != nil {
		return err
	}
	compose := app.Hook{Name: "categories", DependsOn: []string{pool.Name()}, Stop: func(context.Context) error {
		return services.Close()
	}, Start: func(context.Context) error {
		db, err := pool.Value()
		if err != nil {
			return err
		}
		driver, err := entdb.New(db.SQLDB(), entdb.Options{Dialect: s.Config.Database.Driver, Logger: lg})
		if err != nil {
			return err
		}
		repository, err := store.New(ent.NewClient(ent.Driver(driver)))
		if err != nil {
			return err
		}
		catalog, err := category.NewCatalog(repository, func() (category.ID, error) {
			value, err := ids.Next()
			if err != nil {
				return category.ID{}, err
			}
			return category.NewID(value)
		})
		if err != nil {
			return err
		}
		handler, err := newRouter(catalog, s.Verifier, operation.NewSlogSink(lg), checker, lg, s.Config.HTTP.Timeout, ids)
		if err != nil {
			return err
		}
		httpService := server.New(net.JoinHostPort(s.Host, s.Config.HTTP.Port), handler)
		if err := services.Add(app.NamedService{Name: "http", Service: httpService}); err != nil {
			return errors.Join(err, httpService.Close())
		}
		return nil
	}}
	life := s.Config.Lifecycle
	application, err := app.New(services, app.Options{
		Hooks: []app.Hook{pool.Hook(), compose}, StartupTimeout: life.StartupTimeout, ShutdownTimeout: life.ShutdownTimeout, ShutdownDelay: life.ShutdownDelay, CleanupReserve: life.CleanupReserve,
		OnShutdown: func() { checker.SetReady(false) },
	})
	if err != nil {
		return err
	}
	return application.Run(ctx)
}

func openDatabase(cfg appconfig.DatabaseConfig) (*database.DB, error) {
	pool := database.Options{MaxOpenConns: cfg.MaxOpenConns, MaxIdleConns: cfg.MaxIdleConns, ConnMaxIdleTime: cfg.ConnMaxIdleTime, ConnMaxLifetime: cfg.ConnMaxLifetime}
	switch cfg.Driver {
	case "postgres":
		return postgres.Open(postgres.Options{DSN: cfg.URL, Pool: pool})
	case "mysql":
		return mysql.Open(mysql.Options{DSN: cfg.URL, Pool: pool})
	default:
		return nil, errors.New("business: unsupported database driver")
	}
}
