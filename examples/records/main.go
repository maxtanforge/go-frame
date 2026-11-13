// Records demonstrates explicit HTTP -> service -> SQL composition.
// Apply the matching schema manually before starting this program.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/maxtanforge/go-frame/app"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/app/resource"
	"github.com/maxtanforge/go-frame/app/startup"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/database/audit"
	"github.com/maxtanforge/go-frame/database/mysql"
	"github.com/maxtanforge/go-frame/database/postgres"
	"github.com/maxtanforge/go-frame/examples/records/internal/record"
	"github.com/maxtanforge/go-frame/examples/records/internal/sqlstore"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/http/server"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

func main() {
	if err := executeCommands(context.Background(), os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv); err != nil {
		os.Exit(1)
	}
}

func runWithConfig(ctx context.Context, cfg appconfig.Config) error {
	return execute(ctx, func() (appconfig.Config, error) { return cfg, nil }, runApplication)
}

func execute(ctx context.Context, load func() (appconfig.Config, error), run func(context.Context, appconfig.Config, *slog.Logger) error) error {
	return startup.Run(ctx, startup.RunOptions[appconfig.Config]{
		Load: func() (appconfig.Config, error) {
			cfg, err := load()
			if err != nil {
				return cfg, err
			}
			if err := cfg.Validate(); err != nil {
				return cfg, err
			}
			return cfg, validateSettings(cfg)
		},
		Logging: func(cfg appconfig.Config) startup.LogSettings {
			return startup.LogSettings{Service: cfg.Service, Log: cfg.Log}
		}, Run: run,
	})
}
func runApplication(ctx context.Context, cfg appconfig.Config, lg *slog.Logger) error {
	ids, err := id.NewSnowflake(cfg.ID.Node)
	if err != nil {
		return err
	}
	db, err := resource.New(resource.Options[*database.DB]{Name: "database",
		Open:  func(context.Context) (*database.DB, error) { return openDatabase(cfg.Database) },
		Probe: func(ctx context.Context, db *database.DB) error { return db.Ping(ctx) },
		Close: func(_ context.Context, db *database.DB) error { return db.Close() },
	})
	if err != nil {
		return err
	}
	checker, err := health.New(health.Options{Checks: []health.Check{{Name: db.Name(), Probe: db.Probe}}})
	if err != nil {
		return err
	}
	routes := router.New(lg, router.Options{Timeout: cfg.HTTP.Timeout, Health: checker, QuietHealth: true, Trace: []trace.Option{trace.WithSnowflake(ids)}})
	// Register feature routes only after successful resource startup, before Run
	// can accept any requests. No handler accesses a global resource registry.
	compose := app.Hook{Name: "records", Start: func(context.Context) error {
		pool, err := db.Value()
		if err != nil {
			return err
		}
		store, err := sqlstore.New(pool, cfg.Database.Driver)
		if err != nil {
			return err
		}
		record.Register(routes, record.Service{Repository: store, Transactions: store, IDs: ids}, audit.Actor{Kind: audit.System, ID: "records-example"})
		return nil
	}}
	application, err := app.New(server.New(":"+cfg.HTTP.Port, routes), app.Options{
		Hooks: []app.Hook{db.Hook(), compose}, StartupTimeout: cfg.Lifecycle.StartupTimeout,
		ShutdownTimeout: cfg.Lifecycle.ShutdownTimeout, ShutdownDelay: cfg.Lifecycle.ShutdownDelay,
		CleanupReserve: cfg.Lifecycle.CleanupReserve, OnShutdown: func() { checker.SetReady(false) },
	})
	if err != nil {
		return err
	}
	lg.InfoContext(ctx, "application starting", "port", cfg.HTTP.Port)
	return application.Run(ctx)
}

func openDatabase(cfg appconfig.DatabaseConfig) (*database.DB, error) {
	pool := database.Options{MaxOpenConns: cfg.MaxOpenConns, MaxIdleConns: cfg.MaxIdleConns, ConnMaxIdleTime: cfg.ConnMaxIdleTime, ConnMaxLifetime: cfg.ConnMaxLifetime}
	switch cfg.Driver {
	case "mysql":
		dsn, err := prepareMySQLDSN(cfg.URL)
		if err != nil {
			return nil, err
		}
		return mysql.Open(mysql.Options{DSN: dsn, Pool: pool})
	case "postgres":
		return postgres.Open(postgres.Options{DSN: cfg.URL, Pool: pool})
	default:
		return nil, errors.New("records: unsupported database driver")
	}
}

// prepareMySQLDSN keeps driver-side time encoding aligned with audit precision.
func prepareMySQLDSN(dsn string) (string, error) {
	parsed, err := mysqldriver.ParseDSN(dsn)
	if err != nil || !parsed.ParseTime || parsed.Loc != time.UTC {
		return "", errors.New("records: MySQL DSN requires valid parseTime=true&loc=UTC settings")
	}
	if err := parsed.Apply(mysqldriver.TimeTruncate(time.Millisecond)); err != nil {
		return "", errors.New("records: invalid MySQL time precision configuration")
	}
	return parsed.FormatDSN(), nil
}

// Required resources and ID allocation are application policy, checked after
// all input sources. Deployment validation belongs to the caller's loading boundary.
func validateSettings(cfg appconfig.Config) error {
	if cfg.Database.URL == "" {
		return errors.New("records: DATABASE_URL is required; apply the matching schema before startup")
	}
	if cfg.ID.Node < 0 {
		return errors.New("records: ID_NODE is required; assign a distinct node between 0 and 1023 to each generator")
	}
	return nil
}
