package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProductionConfiguration(t *testing.T) {
	c, err := LoadFrom(lookup(map[string]string{
		"STARTUP_TIMEOUT": "45s", "SHUTDOWN_CLEANUP_RESERVE": "3s",
		"REDIS_USERNAME": "service", "REDIS_DB": "2", "REDIS_POOL_SIZE": "12",
		"REDIS_DIAL_TIMEOUT": "2s", "REDIS_READ_TIMEOUT": "4s", "REDIS_WRITE_TIMEOUT": "4s", "REDIS_POOL_TIMEOUT": "1s",
		"REDIS_TLS_ENABLED": "true", "REDIS_TLS_SERVER_NAME": "redis.internal",
		"DB_CONN_MAX_IDLE_TIME": "2m", "DB_CONN_MAX_LIFETIME": "20m",
	}))
	require.NoError(t, err)
	require.Equal(t, 45*time.Second, c.Lifecycle.StartupTimeout)
	require.Equal(t, 3*time.Second, c.Lifecycle.CleanupReserve)
	require.Equal(t, "service", c.Redis.Username)
	require.Equal(t, 2, c.Redis.DB)
	require.Equal(t, 12, c.Redis.PoolSize)
	require.Equal(t, 2*time.Second, c.Redis.DialTimeout)
	require.True(t, c.Redis.TLSEnabled)
	require.Equal(t, "redis.internal", c.Redis.TLSServerName)
	require.Equal(t, 20*time.Minute, c.Database.ConnMaxLifetime)
}

func TestProductionConfigurationValidation(t *testing.T) {
	for _, values := range []map[string]string{
		{"STARTUP_TIMEOUT": "0s"}, {"SHUTDOWN_CLEANUP_RESERVE": "10s"},
		{"SHUTDOWN_DELAY": "8s"}, {"REDIS_DB": "-1"}, {"REDIS_POOL_SIZE": "2147483648"},
		{"REDIS_READ_TIMEOUT": "0s"}, {"REDIS_TLS_ENABLED": "secret"},
		{"REDIS_TLS_CERT_FILE": "secret"}, {"REDIS_TLS_CA_FILE": "secret"},
		{"DB_CONN_MAX_LIFETIME": "-1s"},
		{"REDIS_ADDR": "secret-invalid"}, {"REDIS_ADDR": "cache:0"}, {"REDIS_ADDR": ":6379"},
	} {
		_, err := LoadFrom(lookup(values))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
}
