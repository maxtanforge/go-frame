package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/stretchr/testify/require"
)

func TestRunWithConfigEarlyFailureLoggedOnce(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cfg := appconfig.Defaults()
	cfg.HTTP.Port = "invalid"
	err := runWithConfig(context.Background(), cfg)
	require.Error(t, err)
	require.Equal(t, 1, bytes.Count(output.Bytes(), []byte(`"level":"ERROR"`)))
}

func TestOpenDatabaseSelection(t *testing.T) {
	// Driver selection is a unit test, independent of the developer's PG config.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "PG") {
			t.Setenv(key, "")
		}
	}
	for _, tc := range []struct{ driver, dsn string }{
		{"mysql", "user:secret@tcp(127.0.0.1:1)/example"},
		{"postgres", "postgres://user:secret@127.0.0.1:1/example?sslmode=disable"},
	} {
		t.Run(tc.driver, func(t *testing.T) {
			cfg := appconfig.Defaults().Database
			cfg.Driver, cfg.URL, cfg.MaxOpenConns = tc.driver, tc.dsn, 7
			db, err := openDatabase(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			require.Equal(t, 7, db.SQLDB().Stats().MaxOpenConnections)
			require.Zero(t, db.SQLDB().Stats().OpenConnections)
		})
	}
	cfg := appconfig.Defaults().Database
	cfg.Driver = "unsupported"
	db, err := openDatabase(cfg)
	require.Error(t, err)
	require.Nil(t, db)
}

func TestRunConfigLogsFailureBeforeClosingWithInstanceAttributes(t *testing.T) {
	// Invalid connection syntax fails startup before any network access. The
	// managed logger is already open and must capture the failure before close.
	dir := t.TempDir()
	for _, id := range []string{"replica-a", "replica-b"} {
		values := map[string]string{"DATABASE_URL": "invalid-dsn", "APP_NAME": "orders", "APP_VERSION": "test", "INSTANCE_ID": id, "LOG_FORMAT": "json", "LOG_OUTPUT": "file", "LOG_DIR": dir}
		cfg, err := appconfig.LoadFrom(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = runWithConfig(ctx, cfg)
		cancel()
		require.Error(t, err)
		data, err := os.ReadFile(filepath.Join(dir, id, "service.log"))
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		require.GreaterOrEqual(t, len(lines), 2)
		for _, line := range lines {
			var entry map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &entry))
			require.Equal(t, "orders", entry["service"])
			require.Equal(t, id, entry["instance_id"])
		}
		require.Contains(t, lines[len(lines)-1], "application stopped with error")
	}
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
