// Package config supplies independently composable deployment settings without opening resources.
package config

import "time"

// Config is an optional full service preset. Individual components can be used independently.
// Start with Defaults, override fields, then Validate.
// Database and Redis are optional in every environment.
type Config struct {
	Service   ServiceConfig
	HTTP      HTTPConfig
	Lifecycle LifecycleConfig
	Database  DatabaseConfig
	Redis     RedisConfig
	Log       LogConfig
	ID        IDConfig
}

// IDConfig selects the Snowflake node, defaulting to 0. Multi-instance
// deployments must allocate distinct nodes in the range 0..1023.
type IDConfig struct {
	Node int64 `env:"ID_NODE"`
}

// ServiceConfig carries process identity: environment, name, version and instance ID.
type ServiceConfig struct {
	Env        string `env:"APP_ENV"`                          // APP_ENV: dev (default), test, prod
	Name       string `env:"APP_NAME"`                         // APP_NAME: basic
	Version    string `env:"APP_VERSION"`                      // APP_VERSION: dev
	InstanceID string `config:"instance_id" env:"INSTANCE_ID"` // INSTANCE_ID: process-scoped random ID by default; explicit empty is invalid
}

// HTTPConfig is the listener address and cooperative request timeout.
type HTTPConfig struct {
	Port    string        `env:"HTTP_PORT"`    // HTTP_PORT: 3000
	Timeout time.Duration `env:"HTTP_TIMEOUT"` // HTTP_TIMEOUT: 30s
}

// LifecycleConfig bounds startup, draining and shutdown phases.
type LifecycleConfig struct {
	StartupTimeout  time.Duration `config:"startup_timeout" env:"STARTUP_TIMEOUT"`          // STARTUP_TIMEOUT: 30s
	CleanupReserve  time.Duration `config:"cleanup_reserve" env:"SHUTDOWN_CLEANUP_RESERVE"` // SHUTDOWN_CLEANUP_RESERVE: 2s, within total shutdown budget
	ShutdownTimeout time.Duration `config:"shutdown_timeout" env:"SHUTDOWN_TIMEOUT"`        // SHUTDOWN_TIMEOUT: 10s
	ShutdownDelay   time.Duration `config:"shutdown_delay" env:"SHUTDOWN_DELAY"`            // SHUTDOWN_DELAY: 0s; included in shutdown timeout
}

// DatabaseConfig selects the SQL driver, DSN and connection-pool limits.
type DatabaseConfig struct {
	ConnMaxIdleTime time.Duration `config:"conn_max_idle_time" env:"DB_CONN_MAX_IDLE_TIME"` // DB_CONN_MAX_IDLE_TIME: 5m
	ConnMaxLifetime time.Duration `config:"conn_max_lifetime" env:"DB_CONN_MAX_LIFETIME"`   // DB_CONN_MAX_LIFETIME: 30m
	Driver          string        `env:"DATABASE_DRIVER"`                                   // DATABASE_DRIVER: business-selected adapter, postgres default
	URL             string        `env:"DATABASE_URL" secret:"true"`                        // DATABASE_URL: driver-specific DSN, default empty
	MaxOpenConns    int           `config:"max_open_conns" env:"DB_MAX_OPEN_CONNS"`         // DB_MAX_OPEN_CONNS: 25
	MaxIdleConns    int           `config:"max_idle_conns" env:"DB_MAX_IDLE_CONNS"`         // DB_MAX_IDLE_CONNS: 5
}

// RedisConfig is the Redis endpoint, credentials, TLS and pool behavior.
type RedisConfig struct {
	Username      string        `env:"REDIS_USERNAME"`                                         // REDIS_USERNAME: ACL user
	DB            int           `env:"REDIS_DB"`                                               // REDIS_DB: 0
	PoolSize      int           `config:"pool_size" env:"REDIS_POOL_SIZE"`                     // REDIS_POOL_SIZE: 10
	DialTimeout   time.Duration `config:"dial_timeout" env:"REDIS_DIAL_TIMEOUT"`               // REDIS_DIAL_TIMEOUT: 5s
	ReadTimeout   time.Duration `config:"read_timeout" env:"REDIS_READ_TIMEOUT"`               // REDIS_READ_TIMEOUT: 3s
	WriteTimeout  time.Duration `config:"write_timeout" env:"REDIS_WRITE_TIMEOUT"`             // REDIS_WRITE_TIMEOUT: 3s
	PoolTimeout   time.Duration `config:"pool_timeout" env:"REDIS_POOL_TIMEOUT"`               // REDIS_POOL_TIMEOUT: 4s
	TLSEnabled    bool          `config:"tls_enabled" env:"REDIS_TLS_ENABLED"`                 // REDIS_TLS_ENABLED: false
	TLSCAFile     string        `config:"tls_ca_file" env:"REDIS_TLS_CA_FILE"`                 // REDIS_TLS_CA_FILE: optional additional trust roots
	TLSCertFile   string        `config:"tls_cert_file" env:"REDIS_TLS_CERT_FILE"`             // REDIS_TLS_CERT_FILE: optional client certificate, paired with key
	TLSKeyFile    string        `config:"tls_key_file" env:"REDIS_TLS_KEY_FILE" secret:"true"` // REDIS_TLS_KEY_FILE: optional client private key
	TLSServerName string        `config:"tls_server_name" env:"REDIS_TLS_SERVER_NAME"`         // REDIS_TLS_SERVER_NAME: defaults to connection host
	Addr          string        `env:"REDIS_ADDR"`                                             // REDIS_ADDR: host:port, default empty
	Password      string        `env:"REDIS_PASSWORD" secret:"true"`                           // REDIS_PASSWORD: default empty
	MaxRetries    int           `config:"max_retries" env:"REDIS_MAX_RETRIES"`                 // REDIS_MAX_RETRIES: 0 disables command retries
}

// LogConfig selects level and output sinks for the service logger.
type LogConfig struct {
	Format     string `env:"LOG_FORMAT"`                             // LOG_FORMAT: console (default), json, sls (four-field JSON)
	Level      string `env:"LOG_LEVEL"`                              // LOG_LEVEL: debug, info (default), warn, error
	Output     string `env:"LOG_OUTPUT"`                             // LOG_OUTPUT: stdout (default), file, both
	Dir        string `env:"LOG_DIR"`                                // LOG_DIR: logs; file modes use <dir>/<instance>/service.log
	MaxSizeMB  int    `config:"max_size_mb" env:"LOG_MAX_SIZE_MB"`   // LOG_MAX_SIZE_MB: 100
	MaxBackups int    `config:"max_backups" env:"LOG_MAX_BACKUPS"`   // LOG_MAX_BACKUPS: 10; 0 disables count limit
	MaxAgeDays int    `config:"max_age_days" env:"LOG_MAX_AGE_DAYS"` // LOG_MAX_AGE_DAYS: 7; 0 disables age limit
	Compress   bool   `env:"LOG_COMPRESS"`                           // LOG_COMPRESS: true
	Timezone   string `env:"LOG_TIMEZONE"`                           // LOG_TIMEZONE: UTC; record timestamps and archive names
}
