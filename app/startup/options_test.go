package startup_test

import (
	"path/filepath"
	"testing"
	"time"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/app/startup"
	"github.com/stretchr/testify/require"
)

func TestRuntimeConfigDefaults(t *testing.T) {
	c, err := appconfig.LoadFrom(lookup(nil))
	require.NoError(t, err)
	m, err := c.Service.Metadata()
	require.NoError(t, err)
	require.Equal(t, "basic", m.Service)
	require.Equal(t, "dev", m.Version)
	require.NotEmpty(t, m.InstanceID)
	opts, err := startup.LoggerOptions(startup.LogSettings{Service: c.Service, Log: c.Log})
	require.NoError(t, err)
	require.Nil(t, opts.File)
	require.Equal(t, "console", opts.Format)
	require.False(t, opts.AddSource)
	require.Equal(t, c.Service.Env, opts.Environment)
	require.Equal(t, c.Log.Timezone, opts.Timezone)
	require.Zero(t, c.Lifecycle.ShutdownDelay)
}

func TestSLSConfigEnvironmentAndTimezone(t *testing.T) {
	c, err := appconfig.LoadFrom(lookup(map[string]string{"LOG_FORMAT": "sls", "LOG_TIMEZONE": "Asia/Shanghai", "APP_ENV": "prod"}))
	require.NoError(t, err)
	opts, err := startup.LoggerOptions(startup.LogSettings{Service: c.Service, Log: c.Log})
	require.NoError(t, err)
	require.Equal(t, "sls", opts.Format)
	require.Equal(t, "prod", opts.Environment)
	require.Equal(t, "Asia/Shanghai", opts.Timezone)
	require.False(t, opts.AddSource)
}

func TestRuntimeConfigFileOverrides(t *testing.T) {
	dir := t.TempDir()
	c, err := appconfig.LoadFrom(lookup(map[string]string{
		"APP_NAME": "orders", "APP_VERSION": "2.0", "APP_ENV": "prod", "INSTANCE_ID": "pod-a",
		"LOG_FORMAT": "json", "LOG_OUTPUT": "both", "LOG_DIR": dir, "LOG_MAX_SIZE_MB": "25", "LOG_MAX_BACKUPS": "0",
		"LOG_MAX_AGE_DAYS": "0", "LOG_COMPRESS": "false", "LOG_TIMEZONE": "UTC", "SHUTDOWN_DELAY": "2s",
	}))
	require.NoError(t, err)
	require.Equal(t, 2*time.Second, c.Lifecycle.ShutdownDelay)
	m, err := c.Service.Metadata()
	require.NoError(t, err)
	require.Equal(t, "pod-a", m.InstanceID)
	require.Equal(t, "orders", m.Service)
	opts, err := startup.LoggerOptions(startup.LogSettings{Service: c.Service, Log: c.Log})
	require.NoError(t, err)
	require.True(t, opts.Stdout)
	require.Equal(t, "json", opts.Format)
	require.True(t, opts.AddSource)
	require.Equal(t, filepath.Join(dir, "pod-a", "service.log"), opts.File.Path)
	require.Equal(t, 25, opts.File.MaxSizeMB)
	require.Zero(t, *opts.File.MaxBackups)
	require.Zero(t, *opts.File.MaxAgeDays)
	require.False(t, *opts.File.Compress)
}

func TestRuntimeConfigValidationHidesInput(t *testing.T) {
	for _, key := range []string{"LOG_FORMAT", "LOG_OUTPUT", "LOG_MAX_SIZE_MB", "LOG_MAX_BACKUPS", "LOG_MAX_AGE_DAYS", "LOG_COMPRESS", "LOG_TIMEZONE", "SHUTDOWN_DELAY"} {
		t.Run(key, func(t *testing.T) {
			_, err := appconfig.LoadFrom(lookup(map[string]string{key: "secret-invalid"}))
			require.Error(t, err)
			require.Contains(t, err.Error(), key)
			require.NotContains(t, err.Error(), "secret-invalid")
		})
	}
	for _, values := range []map[string]string{
		{"INSTANCE_ID": "../secret"}, {"LOG_MAX_SIZE_MB": "0"}, {"LOG_MAX_BACKUPS": "-1"},
		{"LOG_MAX_AGE_DAYS": "-1"}, {"SHUTDOWN_DELAY": "-1s"}, {"SHUTDOWN_DELAY": "10s"},
		{"APP_NAME": "   "}, {"LOG_DIR": "   "},
	} {
		_, err := appconfig.LoadFrom(lookup(values))
		require.Error(t, err)
	}
}

func TestReplicaConfigSharesDependenciesNotFiles(t *testing.T) {
	values := map[string]string{"DATABASE_URL": "shared-dsn", "REDIS_ADDR": "cache:6379", "LOG_OUTPUT": "file", "LOG_DIR": t.TempDir(), "INSTANCE_ID": "replica-a"}
	a, err := appconfig.LoadFrom(lookup(values))
	require.NoError(t, err)
	values["INSTANCE_ID"] = "replica-b"
	b, err := appconfig.LoadFrom(lookup(values))
	require.NoError(t, err)
	require.Equal(t, a.Database.URL, b.Database.URL)
	require.Equal(t, a.Redis.Addr, b.Redis.Addr)
	x, err := startup.LoggerOptions(startup.LogSettings{Service: a.Service, Log: a.Log})
	require.NoError(t, err)
	y, err := startup.LoggerOptions(startup.LogSettings{Service: b.Service, Log: b.Log})
	require.NoError(t, err)
	require.NotEqual(t, x.File.Path, y.File.Path)
}

func lookup(values map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := values[k]; return v, ok }
}

func TestOptionsPreserveExplicitValuesWithoutOpeningFiles(t *testing.T) {
	c := appconfig.Defaults()
	c.Log.Output = "file"
	c.Log.Dir = filepath.Join(t.TempDir(), "not-created")
	c.Log.MaxBackups = 0
	c.Log.MaxAgeDays = 0
	c.Log.Compress = false
	before := c
	opts, err := startup.LoggerOptions(startup.LogSettings{Service: c.Service, Log: c.Log})
	require.NoError(t, err)
	require.False(t, opts.Stdout)
	require.Zero(t, *opts.File.MaxBackups)
	require.Zero(t, *opts.File.MaxAgeDays)
	require.False(t, *opts.File.Compress)
	require.Equal(t, before, c)
	require.NoDirExists(t, c.Log.Dir)
	*opts.File.MaxBackups = 42
	require.Equal(t, before, c, "returned options must not alias the caller configuration")
}

func TestOptionsRejectInvalidOutputWithoutEchoingInput(t *testing.T) {
	c := appconfig.Defaults()
	c.Log.Output = "secret-output"
	_, err := startup.LoggerOptions(startup.LogSettings{Service: c.Service, Log: c.Log})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-output")
}
