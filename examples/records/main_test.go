package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/stretchr/testify/require"
)

func TestRunWithConfigEarlyFailureLoggedOnce(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	err := runWithConfig(context.Background(), appconfig.Defaults())
	require.ErrorContains(t, err, "DATABASE_URL is required")
	require.Equal(t, 1, bytes.Count(output.Bytes(), []byte(`"level":"ERROR"`)))
	require.Contains(t, output.String(), err.Error())
}

func TestExecuteLogsLoadFailureOnce(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	want := errors.New("load failed")
	err := execute(context.Background(), func() (appconfig.Config, error) {
		return appconfig.Config{}, want
	}, func(context.Context, appconfig.Config, *slog.Logger) error {
		t.Fatal("application ran after config load failure")
		return nil
	})
	require.ErrorIs(t, err, want)
	require.Equal(t, 1, bytes.Count(output.Bytes(), []byte(`"level":"ERROR"`)))
}

func TestExecuteLogsApplicationFailureOnceWithMetadata(t *testing.T) {
	var fallback bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&fallback, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cfg := appconfig.Defaults()
	cfg.ID.Node = 7
	cfg.Database.URL = "unused: application callback opens no database"
	cfg.Log.Format = "json"
	cfg.Log.Output = "file"
	cfg.Log.Dir = t.TempDir()
	cfg.Service.Name = "records-test"
	want := errors.New("application failed")
	err := execute(context.Background(), func() (appconfig.Config, error) { return cfg, nil },
		func(context.Context, appconfig.Config, *slog.Logger) error { return want })
	require.ErrorIs(t, err, want)
	require.Empty(t, fallback.String())
	meta, err := cfg.Service.Metadata()
	require.NoError(t, err)
	path, err := meta.LogPath(cfg.Log.Dir)
	require.NoError(t, err)
	output, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(output, []byte(`"level":"ERROR"`)))
	require.Contains(t, string(output), "records-test")
	require.Contains(t, string(output), cfg.Service.InstanceID)
	require.Contains(t, string(output), want.Error())
}

func TestExecuteLogsLoggerOpenFailureOnce(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cfg := appconfig.Defaults()
	cfg.ID.Node = 7
	cfg.Database.URL = "unused"
	cfg.Log.Format = "json"
	cfg.Log.Output = "file"
	file, err := os.CreateTemp(t.TempDir(), "not-a-directory")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	cfg.Log.Dir = file.Name()
	err = execute(context.Background(), func() (appconfig.Config, error) { return cfg, nil },
		func(context.Context, appconfig.Config, *slog.Logger) error {
			t.Fatal("application ran after logger open failure")
			return nil
		})
	require.Error(t, err)
	require.Equal(t, 1, bytes.Count(output.Bytes(), []byte(`"level":"ERROR"`)))
}

func TestDatabaseRequired(t *testing.T) {
	err := runWithConfig(context.Background(), appconfig.Defaults())
	require.ErrorContains(t, err, "DATABASE_URL is required")
}

func TestMySQLAuditDSNRequirements(t *testing.T) {
	for _, dsn := range []string{"private-secret", "user:private-secret@tcp(localhost:3306)/example", "user:private-secret@tcp(localhost:3306)/example?parseTime=true&loc=Local", "user:private-secret@tcp(localhost:3306)/example?parseTime=invalid"} {
		db, err := openDatabase(appconfig.DatabaseConfig{Driver: "mysql", URL: dsn})
		require.Error(t, err)
		require.Nil(t, db)
		require.NotContains(t, err.Error(), "private-secret")
	}
	db, err := openDatabase(appconfig.DatabaseConfig{Driver: "mysql", URL: "user:private-secret@tcp(localhost:3306)/example?parseTime=true&loc=UTC"})
	require.NoError(t, err)
	require.NoError(t, db.Close())
}

func TestMySQLDSNEnforcesMillisecondTruncation(t *testing.T) {
	for _, option := range []string{"", "&timeTruncate=1s", "&timeTruncate=7ns"} {
		dsn, err := prepareMySQLDSN("user:private-secret@tcp(localhost:3306)/example?parseTime=true&loc=UTC" + option)
		require.NoError(t, err)
		require.Contains(t, dsn, "timeTruncate=1ms")
		parsed, err := mysqldriver.ParseDSN(dsn)
		require.NoError(t, err)
		require.True(t, parsed.ParseTime)
		require.Same(t, time.UTC, parsed.Loc)
		require.Equal(t, "example", parsed.DBName)
	}
	dsn, err := prepareMySQLDSN("user:private-secret@tcp(localhost:3306)/example?parseTime=true&timeTruncate=private-secret")
	require.Error(t, err)
	require.Empty(t, dsn)
	require.NotContains(t, err.Error(), "private-secret")
}

func TestExecuteValidatesDeploymentBeforeApplication(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invalid func(*appconfig.Config)
	}{
		{"http", func(c *appconfig.Config) { c.HTTP.Timeout = -time.Second }},
		{"lifecycle", func(c *appconfig.Config) { c.Lifecycle.StartupTimeout = -time.Second }},
		{"database", func(c *appconfig.Config) { c.Database.MaxOpenConns = -1 }},
		{"redis", func(c *appconfig.Config) { c.Redis.PoolSize = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := appconfig.Defaults()
			cfg.Database.URL = "unused"
			tc.invalid(&cfg)
			called := false
			err := execute(context.Background(), func() (appconfig.Config, error) { return cfg, nil },
				func(context.Context, appconfig.Config, *slog.Logger) error { called = true; return nil })
			require.Error(t, err)
			require.False(t, called, "invalid deployment configuration reached the application")
		})
	}
}
