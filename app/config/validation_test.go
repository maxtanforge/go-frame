package config

import (
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/config"
	"github.com/stretchr/testify/require"
)

func TestDefaultsValidateWithoutMutation(t *testing.T) {
	c := Defaults()
	c.Log.Output = "file"
	c.Log.Dir = t.TempDir() + "/not-created"
	c.Log.MaxBackups = 0
	c.Log.MaxAgeDays = 0
	c.Log.Compress = false
	c.Redis.MaxRetries = 0
	c.Lifecycle.ShutdownDelay = 0
	before := c
	require.NoError(t, c.Validate())
	require.Equal(t, before, c)
	require.NoDirExists(t, c.Log.Dir)
	require.Equal(t, Defaults().Service.InstanceID, c.Service.InstanceID)
	loaded, err := LoadFrom(lookup(nil))
	require.NoError(t, err)
	require.Equal(t, Defaults(), loaded)
}

func TestProgrammaticValidationMatchesEnvironment(t *testing.T) {
	cases := []struct {
		key, value string
		change     func(*Config)
	}{
		{"APP_ENV", "secret", func(c *Config) { c.Service.Env = "secret" }},
		{"APP_NAME", "   ", func(c *Config) { c.Service.Name = "   " }},
		{"INSTANCE_ID", "../secret", func(c *Config) { c.Service.InstanceID = "../secret" }},
		{"HTTP_PORT", "65536", func(c *Config) { c.HTTP.Port = "65536" }},
		{"HTTP_TIMEOUT", "0s", func(c *Config) { c.HTTP.Timeout = 0 }},
		{"SHUTDOWN_TIMEOUT", "0s", func(c *Config) { c.Lifecycle.ShutdownTimeout = 0 }},
		{"SHUTDOWN_DELAY", "10s", func(c *Config) { c.Lifecycle.ShutdownDelay = 10 * time.Second }},
		{"DB_MAX_OPEN_CONNS", "0", func(c *Config) { c.Database.MaxOpenConns = 0 }},
		{"DB_MAX_IDLE_CONNS", "26", func(c *Config) { c.Database.MaxIdleConns = 26 }},
		{"REDIS_MAX_RETRIES", "-1", func(c *Config) { c.Redis.MaxRetries = -1 }},
		{"LOG_LEVEL", "secret", func(c *Config) { c.Log.Level = "secret" }},
		{"LOG_OUTPUT", "secret", func(c *Config) { c.Log.Output = "secret" }},
		{"LOG_DIR", "   ", func(c *Config) { c.Log.Dir = "   " }},
		{"LOG_MAX_SIZE_MB", "0", func(c *Config) { c.Log.MaxSizeMB = 0 }},
		{"LOG_MAX_BACKUPS", "-1", func(c *Config) { c.Log.MaxBackups = -1 }},
		{"LOG_MAX_AGE_DAYS", "-1", func(c *Config) { c.Log.MaxAgeDays = -1 }},
		{"LOG_TIMEZONE", "secret", func(c *Config) { c.Log.Timezone = "secret" }},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			c := Defaults()
			tc.change(&c)
			before := c
			err := c.Validate()
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			require.Equal(t, before, c)
			_, decodeErr := config.Decode(c)
			var detail *config.FieldError
			require.ErrorAs(t, decodeErr, &detail)
			require.Equal(t, tc.key, detail.Env)
			require.Equal(t, "defaults", detail.Source)
			_, loadErr := LoadFrom(lookup(map[string]string{tc.key: tc.value}))
			var loadedDetail *config.FieldError
			require.ErrorAs(t, loadErr, &loadedDetail)
			require.Equal(t, detail.Path, loadedDetail.Path)
			require.Equal(t, detail.Rule, loadedDetail.Rule)
			require.Equal(t, detail.Env, loadedDetail.Env)
			require.Equal(t, "environment", loadedDetail.Source)
		})
	}
}

func TestEmptyEnvironmentIsPresentAndNilLookupFails(t *testing.T) {
	c, err := LoadFrom(func(string) (string, bool) { return "", true })
	require.Error(t, err)
	require.Equal(t, Config{}, c)
	_, err = LoadFrom(nil)
	require.EqualError(t, err, "config: lookup function is required")
}

func TestProgrammaticEmptyValuesAreNotDefaulted(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Service.InstanceID = "" },
		func(c *Config) { c.HTTP.Port = "" },
		func(c *Config) { c.Database.MaxIdleConns = 0 },
	} {
		c := Defaults()
		change(&c)
		before := c
		require.Error(t, c.Validate())
		require.Equal(t, before, c)
	}
}
