package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func lookup(values map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := values[k]; return v, ok }
}
func TestLoadDefaults(t *testing.T) {
	c, err := LoadFrom(lookup(nil))
	require.NoError(t, err)
	require.Equal(t, "dev", c.Service.Env)
	require.Equal(t, "3000", c.HTTP.Port)
	require.Equal(t, "info", c.Log.Level)
	require.Equal(t, 30*time.Second, c.HTTP.Timeout)
	require.Equal(t, 10*time.Second, c.Lifecycle.ShutdownTimeout)
	require.Equal(t, 25, c.Database.MaxOpenConns)
	require.Equal(t, 5, c.Database.MaxIdleConns)
}
func TestLoadOverridesAndOptionalResources(t *testing.T) {
	c, err := LoadFrom(lookup(map[string]string{"APP_ENV": "prod", "HTTP_PORT": "8080", "LOG_LEVEL": "debug", "HTTP_TIMEOUT": "2s", "SHUTDOWN_TIMEOUT": "3s", "DB_MAX_OPEN_CONNS": "10", "DB_MAX_IDLE_CONNS": "2", "REDIS_PASSWORD": "secret"}))
	require.NoError(t, err)
	require.Empty(t, c.Database.URL)
	require.Equal(t, "8080", c.HTTP.Port)
	require.Equal(t, 2*time.Second, c.HTTP.Timeout)
	require.Equal(t, 3*time.Second, c.Lifecycle.ShutdownTimeout)
	require.Equal(t, 10, c.Database.MaxOpenConns)
	require.Equal(t, 2, c.Database.MaxIdleConns)
	require.Equal(t, "secret", c.Redis.Password)
}
func TestInvalidConfig(t *testing.T) {
	for _, key := range []string{"APP_ENV", "HTTP_PORT", "LOG_LEVEL", "HTTP_TIMEOUT", "SHUTDOWN_TIMEOUT", "DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS"} {
		t.Run(key, func(t *testing.T) {
			_, err := LoadFrom(lookup(map[string]string{key: "secret-invalid-value"}))
			require.Error(t, err)
			require.Contains(t, err.Error(), key)
			require.NotContains(t, err.Error(), "secret-invalid-value")
		})
	}
	for _, port := range []string{"0", "65536", "-1", "+80", "80x"} {
		_, err := LoadFrom(lookup(map[string]string{"HTTP_PORT": port}))
		require.Error(t, err)
	}
	_, err := LoadFrom(lookup(map[string]string{"DB_MAX_OPEN_CONNS": "2", "DB_MAX_IDLE_CONNS": "3"}))
	require.Error(t, err)
	_, err = LoadFrom(lookup(map[string]string{"HTTP_TIMEOUT": "0s"}))
	require.Error(t, err)
}
func TestLoadEnvironment(t *testing.T) {
	t.Setenv("HTTP_PORT", "8088")
	c, err := Load()
	require.NoError(t, err)
	require.Equal(t, "8088", c.HTTP.Port)
}

func TestRedisRetryConfig(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{{"0", 0}, {"2", 2}} {
		c, err := LoadFrom(lookup(map[string]string{"REDIS_MAX_RETRIES": tc.value}))
		require.NoError(t, err)
		require.Equal(t, tc.want, c.Redis.MaxRetries)
	}
	for _, value := range []string{"", "-1", "secret-invalid-value"} {
		_, err := LoadFrom(lookup(map[string]string{"REDIS_MAX_RETRIES": value}))
		require.Error(t, err)
		require.Contains(t, err.Error(), "REDIS_MAX_RETRIES")
		if value != "" {
			require.NotContains(t, err.Error(), value)
		}
	}
}
