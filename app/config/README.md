# Deployment configuration

Build configuration in code with explicit defaults, or load it from the environment:

```go
cfg := appconfig.Defaults()
cfg.Service.Name = "orders"
cfg.HTTP.Port = "8080"
cfg.Log.MaxBackups = 0
cfg.Log.Compress = false
if err := cfg.Validate(); err != nil {
    return err
}
```

`Defaults() Config` does not inspect the environment. Its generated instance ID is
stable across calls in one process and shared with the identity package.
`Config.Validate() error` checks values without changing them, filling defaults,
opening connections or creating log files. Set overrides after calling Defaults;
valid zero values and `false` remain explicit. A bare `Config{}` is not valid.
An empty programmatic instance ID is invalid; Defaults supplies the generated ID.

`Load()` reads the process environment. `LoadFrom(lookup)` starts from Defaults,
decodes all present values and calls the same Validate method through generic `config.Decode`. Absent values keep
defaults; explicit empty strings override them, and empty numeric/bool values fail. Errors name environment keys without echoing
potentially sensitive values. Empty database/Redis endpoints do not enable providers,
but the full preset validates every component. A service-owned struct can compose
only required component types and validate optional providers when enabled; see
[service template](../../templates/service/README.md).

| Component type / field | Settings | Environment keys |
| --- | --- | --- |
| `ServiceConfig` / `Service` | `Env`, `Name`, `Version`, `InstanceID` | `APP_ENV`, `APP_NAME`, `APP_VERSION`, `INSTANCE_ID` |
| `HTTPConfig` / `HTTP` | `Port`, `Timeout` | `HTTP_PORT`, `HTTP_TIMEOUT` |
| `IDConfig` / `ID` | `Node` (default 0; range 0..1023) | `ID_NODE` |
| `LifecycleConfig` / `Lifecycle` | Startup, shutdown, withdrawal and cleanup reserve | `STARTUP_TIMEOUT`, `SHUTDOWN_TIMEOUT`, `SHUTDOWN_DELAY`, `SHUTDOWN_CLEANUP_RESERVE` |
| `DatabaseConfig` / `Database` | Driver, URL, pool limits and connection lifetimes | `DATABASE_DRIVER`, `DATABASE_URL`, `DB_MAX_*`, `DB_CONN_MAX_*` (full names below and in config.go) |
| `RedisConfig` / `Redis` | Address, ACL, database, pool, retry, timeouts and TLS | `REDIS_*` (full names below and in config.go) |
| `LogConfig` / `Log` | `Format`, `Level`, `Output`, `Dir`, `MaxSizeMB`, `MaxBackups`, `MaxAgeDays`, `Compress`, `Timezone` | `LOG_FORMAT`, `LOG_LEVEL`, `LOG_OUTPUT`, `LOG_DIR`, `LOG_MAX_SIZE_MB`, `LOG_MAX_BACKUPS`, `LOG_MAX_AGE_DAYS`, `LOG_COMPRESS`, `LOG_TIMEZONE` |

Defaults and constraints remain documented beside each field. Database pool
limits must be positive, with idle connections no greater than open connections.
Zero retries disable Redis command retries. Zero log backup or age limits disable
the corresponding retention limit. Shutdown delay may be zero and must be less
than the positive shutdown timeout.

`DATABASE_DRIVER` is a non-empty business-selected adapter name; `postgres` is the
recommended default. Configuration imposes no driver whitelist and preserves the
optional `DATABASE_URL` as a driver-specific DSN. The application chooses which
adapters it supports and lets its selected adapter parse the DSN at composition time.
Configuration does not import a database driver or open a connection.

`ServiceConfig.Metadata()` maps service settings to identity metadata. The separate
`app/startup` package exposes `startup.LoggerOptions(startup.LogSettings{Service: cfg.Service, Log: cfg.Log})`, which validates and maps
logging settings to runtime logger options without creating files. Call
`cfg.Validate()` for the full preset or validate the selected components. Importing `app/config` alone
does not import the logger implementation. Timezone validation embeds Go's
timezone data so it also works on hosts without a zoneinfo installation.
File logging uses `<Log.Dir>/<Service.InstanceID>/service.log`.

`app/startup.Run` also owns loading diagnostics, metadata-enriched logging and
cleanup around an application callback; see [startup composition](../startup/README.md).
Programs using Snowflake must assign distinct nodes. See [ID deployment constraints](../../id/README.md).

## Production options and adapters

| Environment | Default | Meaning |
|---|---|---|
| STARTUP_TIMEOUT | 30s | Shared resource-start budget |
| SHUTDOWN_CLEANUP_RESERVE | 2s | Reserved within SHUTDOWN_TIMEOUT; 0 disables |
| DB_CONN_MAX_IDLE_TIME / DB_CONN_MAX_LIFETIME | 5m / 30m | SQL idle and total connection lifetime |
| REDIS_USERNAME / REDIS_DB | empty / 0 | ACL user and database index |
| REDIS_POOL_SIZE | 10 | Command connection pool limit |
| REDIS_DIAL_TIMEOUT / REDIS_READ_TIMEOUT / REDIS_WRITE_TIMEOUT / REDIS_POOL_TIMEOUT | 5s / 3s / 3s / 4s | Redis time budgets |
| REDIS_TLS_ENABLED | false | Enable verified TLS, minimum TLS 1.2 |
| REDIS_TLS_CA_FILE | empty | Add PEM CA roots to system trust |
| REDIS_TLS_CERT_FILE / REDIS_TLS_KEY_FILE | empty | Paired mTLS client certificate and key |
| REDIS_TLS_SERVER_NAME | empty | Verify this name; otherwise driver uses host |

Shutdown delay plus cleanup reserve must be less than the total shutdown timeout.
All connection lifetimes/timeouts above must be positive. TLS-specific settings
require TLS enabled; certificate/key must be paired. `Validate` does not read files.

`app/config/redis.Options(cfg.Redis)`
validates Redis settings and loads configured TLS files without connecting. Call
it in the resource startup factory, then pass the result to `redis.Open`. Parsing
errors omit file paths and key material. The adapter is not imported by `app/config`.


## Compose individual components

Import this package as `appconfig "github.com/maxtanforge/go-frame/app/config"`.
`Config` is an optional full service preset. Each component exposes a pure
`Validate() error` method, so a worker can include only `ServiceConfig` and
`LogConfig`, or a service can compose its own HTTP, lifecycle and database fields.
`Defaults()` supplies independently usable component values through its fields.

Use the generic `config` package for decoding and diagnostics:

```go
type WorkerConfig struct {
    Service appconfig.ServiceConfig
    Log appconfig.LogConfig
}

func (c WorkerConfig) Validate() error {
    if err := c.Service.Validate(); err != nil {
        return config.PrefixValidation("service", err)
    }
    return config.PrefixValidation("log", c.Log.Validate())
}

preset := appconfig.Defaults()
result, err := config.Decode(
    WorkerConfig{Service: preset.Service, Log: preset.Log},
    config.Source{Name: "environment", Lookup: os.LookupEnv},
)
```

Direct validation returns opaque errors. `config.Decode` resolves declared field
paths, environment keys, winning sources and rule categories into
`*config.FieldError`, while excluding supplied values. `LoadFrom` uses that same
decoder for the full preset. See [generic decoding and diagnostics](../../config/README.md).
