// Apply the selected versioned migration before running.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/maxtanforge/go-frame/app"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/app/resource"
	"github.com/maxtanforge/go-frame/app/startup"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/database/audit"
	auditprincipal "github.com/maxtanforge/go-frame/database/audit/principal"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/database/mysql"
	"github.com/maxtanforge/go-frame/database/postgres"
	"github.com/maxtanforge/go-frame/examples/ent/ent"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/http/server"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/telemetry/metric"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

type applicationSettings struct {
	appconfig.Config
	Verifier auth.Verifier
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := startup.Run(ctx, startup.RunOptions[applicationSettings]{
		Load: func() (applicationSettings, error) {
			cfg, err := appconfig.Load()
			if err != nil {
				return applicationSettings{}, err
			}
			if err := validateSettings(cfg); err != nil {
				return applicationSettings{}, err
			}
			verifier, err := loadVerifier(os.Getenv)
			if err != nil {
				return applicationSettings{}, err
			}
			return applicationSettings{Config: cfg, Verifier: verifier}, nil
		},
		Logging: func(cfg applicationSettings) startup.LogSettings {
			return startup.LogSettings{Service: cfg.Service, Log: cfg.Log}
		}, Run: runApplication,
	}); err != nil {
		os.Exit(1)
	}
}
func validateSettings(cfg appconfig.Config) error {
	if cfg.Database.URL == "" {
		return errors.New("ent example: DATABASE_URL is required; apply schema before startup")
	}
	if cfg.ID.Node < 0 {
		return errors.New("ent example: ID_NODE is required and must be unique per generator")
	}
	return nil
}
func runApplication(ctx context.Context, cfg applicationSettings, lg *slog.Logger) error {
	if err := validateSettings(cfg.Config); err != nil {
		return err
	}
	metrics, err := metric.NewRequestRecorder(metric.Options{})
	if err != nil {
		return err
	}
	ids, err := id.NewSnowflake(cfg.ID.Node)
	if err != nil {
		return err
	}
	pool, err := resource.New(resource.Options[*database.DB]{Name: "database",
		Open:  func(context.Context) (*database.DB, error) { return openDatabase(cfg.Database) },
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
	// Compile policy and observers before app.Run opens the database resource.
	routes, err := newRoutes(lg, router.Options{Timeout: cfg.HTTP.Timeout, Health: checker, QuietHealth: true, Trace: []trace.Option{trace.WithSnowflake(ids)}}, cfg.Verifier, operation.NewSlogSink(lg), metrics)
	if err != nil {
		return err
	}
	compose := app.Hook{Name: "products", Start: func(context.Context) error {
		db, err := pool.Value()
		if err != nil {
			return err
		}
		orm, err := newClient(db.SQLDB(), cfg.Database.Driver, entdb.Options{Logger: lg, SlowThreshold: 200 * time.Millisecond}, entdb.MutationOptions{IDs: ids, Actor: auditprincipal.FromContext})
		if err != nil {
			return err
		}
		service, err := NewService(orm)
		if err != nil {
			return err
		}
		return Register(routes, service)
	}}
	application, err := app.New(server.New(":"+cfg.HTTP.Port, routes), app.Options{
		Hooks: []app.Hook{pool.Hook(), compose}, StartupTimeout: cfg.Lifecycle.StartupTimeout,
		ShutdownTimeout: cfg.Lifecycle.ShutdownTimeout, ShutdownDelay: cfg.Lifecycle.ShutdownDelay, CleanupReserve: cfg.Lifecycle.CleanupReserve,
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
	case "mysql":
		dsn, err := mysqldriver.ParseDSN(cfg.URL)
		if err != nil || !dsn.ParseTime || dsn.Loc != time.UTC {
			return nil, errors.New("ent example: MySQL DSN requires parseTime=true&loc=UTC")
		}
		if err := dsn.Apply(mysqldriver.TimeTruncate(time.Millisecond)); err != nil {
			return nil, err
		}
		return mysql.Open(mysql.Options{DSN: dsn.FormatDSN(), Pool: pool})
	case "postgres":
		return postgres.Open(postgres.Options{DSN: cfg.URL, Pool: pool})
	default:
		return nil, errors.New("ent example: unsupported database driver")
	}
}

// This example supports both engines; a business imports only its chosen pool driver.
func newClient(pool *sql.DB, driver string, opts entdb.Options, mutations entdb.MutationOptions) (*ent.Client, error) {
	opts.Dialect = driver
	drv, err := entdb.New(pool, opts)
	if err != nil {
		return nil, err
	}
	client := ent.NewClient(ent.Driver(drv))
	if err := installMutationHooks(client, mutations); err != nil {
		return nil, err
	}
	return client, nil
}
func exampleActor(context.Context) (audit.Actor, error) {
	return audit.Actor{Kind: audit.System, ID: systemActor}, nil
}

func installMutationHooks(client *ent.Client, opts entdb.MutationOptions) error {
	ids, err := entdb.NewIDHook(opts.IDs)
	if err != nil {
		return err
	}
	auditHook, err := entdb.NewAuditHook(entdb.AuditOptions{Actor: opts.Actor, Now: opts.Now})
	if err != nil {
		return err
	}
	client.Use(ids, auditHook)
	return nil
}
