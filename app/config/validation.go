package config

import (
	"math"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Keep timezone validation independent of host zoneinfo installation.

	"github.com/maxtanforge/go-frame/config"
	"github.com/maxtanforge/go-frame/identity"
)

// Validate checks the optional full service preset without mutation or I/O.
// Individual components can be composed and validated independently.
func (c Config) Validate() error {
	for _, component := range []struct {
		path     string
		validate func() error
	}{
		{"id", c.ID.Validate}, {"service", c.Service.Validate},
		{"http", c.HTTP.Validate}, {"lifecycle", c.Lifecycle.Validate},
		{"database", c.Database.Validate}, {"redis", c.Redis.Validate}, {"log", c.Log.Validate},
	} {
		if err := component.validate(); err != nil {
			return config.PrefixValidation(component.path, err)
		}
	}
	return nil
}

// Validate checks the Snowflake node without changing settings.
func (c IDConfig) Validate() error {
	if c.Node < 0 || c.Node > 1023 {
		return config.Invalid("node", config.RuleRange)
	}
	return nil
}

// Validate checks service identity without opening resources.
func (c ServiceConfig) Validate() error {
	switch c.Env {
	case "dev", "test", "prod":
	default:
		return config.Invalid("env", config.RuleChoice)
	}
	if strings.TrimSpace(c.Name) == "" {
		return config.Invalid("name", config.RuleRequired)
	}
	if err := identity.ValidateInstanceID(c.InstanceID); err != nil {
		return config.Invalid("instance_id", config.RuleFormat)
	}
	return nil
}

// Validate checks listener and request timeout settings without opening a listener.
func (c HTTPConfig) Validate() error {
	for _, r := range c.Port {
		if r < '0' || r > '9' {
			return config.Invalid("port", config.RuleFormat)
		}
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return config.Invalid("port", config.RuleRange)
	}
	if c.Timeout <= 0 {
		return config.Invalid("timeout", config.RuleRange)
	}
	return nil
}

// Validate checks lifecycle budgets without starting lifecycle operations.
func (c LifecycleConfig) Validate() error {
	if c.StartupTimeout <= 0 {
		return config.Invalid("startup_timeout", config.RuleRange)
	}
	if c.ShutdownTimeout <= 0 {
		return config.Invalid("shutdown_timeout", config.RuleRange)
	}
	if c.ShutdownDelay < 0 || c.ShutdownDelay >= c.ShutdownTimeout {
		return config.Invalid("shutdown_delay", config.RuleRelation)
	}
	if c.CleanupReserve < 0 || c.CleanupReserve >= c.ShutdownTimeout-c.ShutdownDelay {
		return config.Invalid("cleanup_reserve", config.RuleRelation)
	}
	return nil
}

// Validate checks connection settings without selecting an adapter or opening a database.
func (c DatabaseConfig) Validate() error {
	if c.MaxOpenConns <= 0 {
		return config.Invalid("max_open_conns", config.RuleRange)
	}
	if c.MaxIdleConns <= 0 {
		return config.Invalid("max_idle_conns", config.RuleRange)
	}
	if strings.TrimSpace(c.Driver) == "" {
		return config.Invalid("driver", config.RuleRequired)
	}
	if c.MaxIdleConns > c.MaxOpenConns {
		return config.Invalid("max_idle_conns", config.RuleRelation)
	}
	if c.ConnMaxIdleTime <= 0 {
		return config.Invalid("conn_max_idle_time", config.RuleRange)
	}
	if c.ConnMaxLifetime <= 0 {
		return config.Invalid("conn_max_lifetime", config.RuleRange)
	}
	return nil
}

// Validate checks logging settings without creating directories or opening files.
func (c LogConfig) Validate() error {
	switch c.Level {
	case "debug", "info", "warn", "error":
	default:
		return config.Invalid("level", config.RuleChoice)
	}
	switch c.Format {
	case "console", "json", "sls":
	default:
		return config.Invalid("format", config.RuleChoice)
	}
	switch c.Output {
	case "stdout", "file", "both":
	default:
		return config.Invalid("output", config.RuleChoice)
	}
	if strings.TrimSpace(c.Dir) == "" {
		return config.Invalid("dir", config.RuleRequired)
	}
	if c.MaxSizeMB < 1 || int64(c.MaxSizeMB) > math.MaxInt64/(1<<20) {
		return config.Invalid("max_size_mb", config.RuleRange)
	}
	if c.MaxBackups < 0 {
		return config.Invalid("max_backups", config.RuleRange)
	}
	if c.MaxAgeDays < 0 {
		return config.Invalid("max_age_days", config.RuleRange)
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return config.Invalid("timezone", config.RuleFormat)
	}
	return nil
}
