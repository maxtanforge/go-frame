package config

import (
	"time"

	"github.com/maxtanforge/go-frame/identity"
)

// Defaults returns an independent configuration with a stable process identity.
// It reads no environment variables and opens no application resources.
// identity.New cannot fail for these constant inputs today (rand.Text emits
// only the base32 alphabet, which always survives ValidateInstanceID). If that
// ever changes, the fallback keeps a valid static instance ID instead of
// panicking out of the library.
func Defaults() Config {
	metadata, err := identity.New(identity.Options{Service: "basic", Version: "dev", Environment: "dev"})
	if err != nil {
		metadata = identity.Metadata{Service: "basic", Version: "dev", Environment: "dev", InstanceID: "instance"}
	}
	return Config{
		ID:        IDConfig{Node: 0},
		Service:   ServiceConfig{Env: "dev", Name: "basic", Version: "dev", InstanceID: metadata.InstanceID},
		HTTP:      HTTPConfig{Port: "3000", Timeout: 30 * time.Second},
		Lifecycle: LifecycleConfig{StartupTimeout: 30 * time.Second, ShutdownTimeout: 10 * time.Second, CleanupReserve: 2 * time.Second},
		Database:  DatabaseConfig{Driver: "postgres", MaxOpenConns: 25, MaxIdleConns: 5, ConnMaxIdleTime: 5 * time.Minute, ConnMaxLifetime: 30 * time.Minute},
		Redis:     RedisConfig{PoolSize: 10, DialTimeout: 5 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, PoolTimeout: 4 * time.Second},
		Log:       LogConfig{Format: "console", Level: "info", Output: "stdout", Dir: "logs", MaxSizeMB: 100, MaxBackups: 10, MaxAgeDays: 7, Compress: true, Timezone: "UTC"},
	}
}
