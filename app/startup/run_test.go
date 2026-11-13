package startup

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/config"
)

func TestRunRejectsInvalidLoggingComponentsBeforeCallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*LogSettings)
	}{
		{"service", func(c *LogSettings) { c.Service.InstanceID = "../secret" }},
		{"log", func(c *LogSettings) { c.Log.Level = "secret" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defaults := appconfig.Defaults()
			settings := LogSettings{Service: defaults.Service, Log: defaults.Log}
			tc.change(&settings)
			var out bytes.Buffer
			err := Run(context.Background(), RunOptions[LogSettings]{
				Load:    func() (LogSettings, error) { return settings, nil },
				Logging: func(c LogSettings) LogSettings { return c },
				Run: func(context.Context, LogSettings, *slog.Logger) error {
					t.Fatal("callback after invalid settings")
					return nil
				},
				Fallback: slog.New(slog.NewJSONHandler(&out, nil)),
			})
			if err == nil || strings.Contains(out.String(), "secret") || bytes.Count(out.Bytes(), []byte(`"level":"ERROR"`)) != 1 {
				t.Fatalf("invalid settings result: %v; output: %s", err, &out)
			}
		})
	}
}

func TestRunPreservesConfigurationFieldDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		key, value, path string
		rule             config.Rule
	}{
		{"APP_NAME", " ", "service.name", config.RuleRequired},
		{"LOG_LEVEL", "secret-level", "log.level", config.RuleChoice},
	} {
		t.Run(tc.key, func(t *testing.T) {
			_, loadErr := appconfig.LoadFrom(func(key string) (string, bool) { return tc.value, key == tc.key })
			var out bytes.Buffer
			err := Run(context.Background(), RunOptions[appconfig.Config]{
				Load:    func() (appconfig.Config, error) { return appconfig.Config{}, loadErr },
				Logging: func(appconfig.Config) LogSettings { t.Fatal("logging after load failure"); return LogSettings{} },
				Run: func(context.Context, appconfig.Config, *slog.Logger) error {
					t.Fatal("run after load failure")
					return nil
				},
				Fallback: slog.New(slog.NewJSONHandler(&out, nil)),
			})
			var detail *config.FieldError
			if !errors.Is(err, loadErr) || !errors.As(err, &detail) {
				t.Fatalf("original loading diagnostic lost: %v", err)
			}
			if detail.Path != tc.path || detail.Env != tc.key || detail.Source != "environment" || detail.Rule != tc.rule || strings.Contains(out.String(), "secret-level") {
				t.Fatalf("unsafe or incorrect diagnostic: %+v; output: %s", detail, &out)
			}
		})
	}
}

func TestRunDoesNotRequireHTTPDatabaseOrIDs(t *testing.T) {
	cfg := appconfig.Defaults()
	cfg.HTTP.Port = "unused"
	cfg.Database.MaxOpenConns = -1
	cfg.ID.Node = -1
	called := false
	err := Run(context.Background(), RunOptions[appconfig.Config]{
		Load:    func() (appconfig.Config, error) { return cfg, nil },
		Logging: func(c appconfig.Config) LogSettings { return LogSettings{Service: c.Service, Log: c.Log} },
		Run:     func(context.Context, appconfig.Config, *slog.Logger) error { called = true; return nil },
	})
	if err != nil || !called {
		t.Fatalf("logging startup required unused components: called=%v err=%v", called, err)
	}
}

func TestRunOwnsErrorsAndLogLifetime(t *testing.T) {
	var fallback bytes.Buffer
	backup := slog.New(slog.NewJSONHandler(&fallback, nil))
	want := errors.New("load failed")
	options := RunOptions[appconfig.Config]{Fallback: backup, Load: func() (appconfig.Config, error) { return appconfig.Config{}, want }, Logging: func(c appconfig.Config) LogSettings { return LogSettings{Service: c.Service, Log: c.Log} }, Run: func(context.Context, appconfig.Config, *slog.Logger) error { t.Fatal("unexpected run"); return nil }}
	if err := Run(context.Background(), options); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if bytes.Count(fallback.Bytes(), []byte(`"level":"ERROR"`)) != 1 {
		t.Fatal(fallback.String())
	}
	fallback.Reset()
	cfg := appconfig.Defaults()
	cfg.Log.Format = "json"
	cfg.Log.Output = "file"
	cfg.Log.Dir = t.TempDir()
	cfg.Service.Name = "bootstrap-test"
	options.Load = func() (appconfig.Config, error) { return cfg, nil }
	options.Run = func(_ context.Context, _ appconfig.Config, lg *slog.Logger) error { lg.Info("running"); return want }
	if err := Run(context.Background(), options); !errors.Is(err, want) {
		t.Fatal(err)
	}
	meta, _ := cfg.Service.Metadata()
	path, _ := meta.LogPath(cfg.Log.Dir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Len() != 0 || bytes.Count(data, []byte(`"level":"ERROR"`)) != 1 || !bytes.Contains(data, []byte(`"service":"bootstrap-test"`)) {
		t.Fatalf("logs: %s fallback: %s", data, &fallback)
	}
}

func TestRunRejectsInvalidCallbacksAndCanceledContext(t *testing.T) {
	var out bytes.Buffer
	opts := RunOptions[int]{Fallback: slog.New(slog.NewJSONHandler(&out, nil))}
	if Run(context.Background(), opts) == nil {
		t.Fatal("missing callbacks accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opts.Load = func() (int, error) { t.Fatal("load after cancel"); return 0, nil }
	opts.Logging = func(int) LogSettings { c := appconfig.Defaults(); return LogSettings{Service: c.Service, Log: c.Log} }
	opts.Run = func(context.Context, int, *slog.Logger) error { return nil }
	if err := Run(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRunLoggedPreservesCloseAndPanic(t *testing.T) {
	var out, backup bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&out, nil))
	fallback := slog.New(slog.NewJSONHandler(&backup, nil))
	runErr, closeErr := errors.New("run failed"), errors.New("close failed")
	closes := 0
	closeLogs := func() error {
		closes++
		if !strings.Contains(out.String(), "run failed") {
			t.Error("closed before outcome logged")
		}
		return closeErr
	}
	err := runLogged(lg, fallback, closeLogs, func() error { return runErr })
	if !errors.Is(err, runErr) || !errors.Is(err, closeErr) || closes != 1 {
		t.Fatal(err, closes)
	}
	if bytes.Count(out.Bytes(), []byte(`"level":"ERROR"`)) != 1 || bytes.Count(backup.Bytes(), []byte(`"level":"ERROR"`)) != 1 {
		t.Fatal("duplicate errors")
	}
	out.Reset()
	closes = 0
	func() {
		defer func() {
			if recover() != runErr { //nolint:errorlint // panic 载荷身份断言
				t.Error("panic changed")
			}
		}()
		_ = runLogged(lg, fallback, func() error { closes++; return nil }, func() error { panic(runErr) })
	}()
	if closes != 1 || strings.Contains(out.String(), "application stopped") {
		t.Fatal("panic cleanup", closes, out.String())
	}
}

func TestRunLoggedNormalReturnAndCloseFailure(t *testing.T) {
	for _, failClose := range []bool{false, true} {
		var out, backup bytes.Buffer
		lg := slog.New(slog.NewJSONHandler(&out, nil))
		fallback := slog.New(slog.NewJSONHandler(&backup, nil))
		failure := errors.New("close failed")
		closes := 0
		err := runLogged(lg, fallback, func() error {
			closes++
			if !strings.Contains(out.String(), "application stopped") {
				t.Error("outcome missing before close")
			}
			if failClose {
				return failure
			}
			return nil
		}, func() error { return nil })
		if closes != 1 || bytes.Count(out.Bytes(), []byte(`"level":"INFO"`)) != 1 {
			t.Fatal("normal outcome", closes, out.String())
		}
		if failClose {
			if !errors.Is(err, failure) || bytes.Count(backup.Bytes(), []byte(`"level":"ERROR"`)) != 1 {
				t.Fatal(err, backup.String())
			}
		} else if err != nil || backup.Len() != 0 {
			t.Fatal(err, backup.String())
		}
	}
}
