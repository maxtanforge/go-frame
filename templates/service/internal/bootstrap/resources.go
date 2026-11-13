package bootstrap

import (
	"context"
	"errors"

	"github.com/maxtanforge/go-frame/app"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	redisconfig "github.com/maxtanforge/go-frame/app/config/redis"
	"github.com/maxtanforge/go-frame/app/resource"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/database/mysql"
	"github.com/maxtanforge/go-frame/database/postgres"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/redis"
)

type resources struct {
	database *resource.Resource[*database.DB]
	cache    *resource.Resource[*redis.Pool]
}

// dependencies contains borrowed values, valid until application shutdown.
// A nil field means the corresponding optional dependency is not configured.
type dependencies struct {
	Database *database.DB
	Cache    *redis.Pool
}

func (r resources) hooks() []app.Hook {
	var hooks []app.Hook
	if r.database != nil {
		hooks = append(hooks, r.database.Hook())
	}
	if r.cache != nil {
		hooks = append(hooks, r.cache.Hook())
	}
	return hooks
}

func (r resources) checks() []health.Check {
	var checks []health.Check
	if r.database != nil {
		checks = append(checks, health.Check{Name: r.database.Name(), Probe: r.database.Probe})
	}
	if r.cache != nil {
		checks = append(checks, health.Check{Name: r.cache.Name(), Probe: r.cache.Probe})
	}
	return checks
}

func (r resources) resolve() (dependencies, error) {
	var deps dependencies
	var err error
	if r.database != nil {
		deps.Database, err = r.database.Value()
		if err != nil {
			return dependencies{}, err
		}
	}
	if r.cache != nil {
		deps.Cache, err = r.cache.Value()
		if err != nil {
			return dependencies{}, err
		}
	}
	return deps, nil
}

// Declare dependencies explicitly: app compiles startup before serving.
func (r resources) compositionHook(services *app.Group, compose func(context.Context, dependencies, *app.Cleanup) error) app.Hook {
	var cleanup app.Cleanup
	var dependsOn []string
	if r.database != nil {
		dependsOn = append(dependsOn, r.database.Name())
	}
	if r.cache != nil {
		dependsOn = append(dependsOn, r.cache.Name())
	}
	return app.Hook{Name: "compose", DependsOn: dependsOn, Stop: func(ctx context.Context) error {
		// Startup rollback also closes accepted services before their resources.
		return errors.Join(services.Close(), cleanup.Stop(ctx))
	}, Start: func(ctx context.Context) error {
		deps, err := r.resolve()
		if err != nil {
			return err
		}
		return compose(ctx, deps, &cleanup)
	}}
}

// Construction is lazy: app starts/probes resources before serving and closes
// them in reverse order after HTTP drain, including startup rollback.
func newResources(cfg runtimeSettings) (resources, error) {
	var owned resources
	if cfg.Database.URL != "" {
		db, err := resource.New(resource.Options[*database.DB]{Name: "database", Open: func(context.Context) (*database.DB, error) { return openDatabase(cfg.Database) }, Probe: func(ctx context.Context, db *database.DB) error { return db.Ping(ctx) }, Close: func(_ context.Context, db *database.DB) error { return db.Close() }})
		if err != nil {
			return resources{}, err
		}
		owned.database = db
	}
	if cfg.Redis.Addr != "" {
		cache, err := resource.New(resource.Options[*redis.Pool]{Name: "redis", Open: func(context.Context) (*redis.Pool, error) {
			opts, err := redisconfig.Options(cfg.Redis)
			if err != nil {
				return nil, err
			}
			return redis.Open(opts)
		}, Probe: func(ctx context.Context, cache *redis.Pool) error { return cache.Ping(ctx) }, Close: func(_ context.Context, cache *redis.Pool) error { return cache.Close() }})
		if err != nil {
			return resources{}, err
		}
		owned.cache = cache
	}
	return owned, nil
}

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
