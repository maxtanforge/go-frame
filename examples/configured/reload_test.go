package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/config/reload"
)

func TestReloadWhitelistAndFrozenInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("business:\n  message: initial\n")
	env := map[string]string{"BUSINESS_API_KEY": "initial-secret"}
	in, err := newInputs([]string{"--config", path, "--watch-config"}, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	initial, err := in.Load()
	if err != nil {
		t.Fatal(err)
	}
	applied := ""
	manager, err := newSettingsManager(initial.Value, func(level string) { applied = level })
	if err != nil {
		t.Fatal(err)
	}
	env["BUSINESS_API_KEY"] = "new-process-value"
	write("framework:\n  log:\n    level: debug\nbusiness:\n  message: changed\n  labels: [one]\n")
	changed, err := reloadSettings(context.Background(), in, manager)
	if err != nil || len(changed) != 3 {
		t.Fatalf("%v %v", changed, err)
	}
	s := manager.Snapshot()
	if s.Version != 2 || s.Value.Business.Message != "changed" || s.Value.Business.APIKey != "initial-secret" || applied != "debug" {
		t.Fatal("invalid applied snapshot")
	}
	s.Value.Business.Labels[0] = "mutated"
	if manager.Snapshot().Value.Business.Labels[0] != "one" {
		t.Fatal("snapshot is mutable")
	}
	if changed, err := reloadSettings(context.Background(), in, manager); err != nil || len(changed) != 0 {
		t.Fatal("identical effective config advanced revision")
	}
	write("framework:\n  http:\n    port: '3001'\nbusiness:\n  message: forbidden-mixed\n")
	if _, err := reloadSettings(context.Background(), in, manager); !errors.Is(err, reload.ErrRestartRequired) {
		t.Fatalf("static change accepted: %v", err)
	}
	if manager.Snapshot().Version != 2 || applied != "debug" {
		t.Fatal("rejected update affected runtime")
	}
	write("business:\n  message: [invalid\n")
	if _, err := reloadSettings(context.Background(), in, manager); err == nil {
		t.Fatal("invalid file accepted")
	}
	if manager.Snapshot().Version != 2 {
		t.Fatal("invalid revision published")
	}
}

func TestWatchFlagRequiresExplicitFile(t *testing.T) {
	if _, err := newInputs([]string{"--watch-config"}, noEnv); err == nil {
		t.Fatal("watch without files accepted")
	}
}

func TestAllStaticFieldsRejectTogether(t *testing.T) {
	in, err := newInputs(nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	r, err := in.Load()
	if err != nil {
		t.Fatal(err)
	}
	m, err := newSettingsManager(r.Value, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*settings){func(s *settings) { s.Business.APIKey = "new-secret" }, func(s *settings) { s.Host = "0.0.0.0" }, func(s *settings) { s.Framework.Database.URL = "secret-dsn" }} {
		c := cloneSettings(r.Value)
		change(&c)
		c.Business.Message = "also changed"
		if _, _, err := m.Update(context.Background(), c); !errors.Is(err, reload.ErrRestartRequired) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("bad static rejection: %v", err)
		}
	}
}

func TestFileWatcherReconcilesStartupAndRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("business:\n  message: initial\n")
	in, err := newInputs([]string{"--config", path, "--watch-config"}, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := in.Load()
	if err != nil {
		t.Fatal(err)
	}
	m, err := newSettingsManager(initial.Value, nil)
	if err != nil {
		t.Fatal(err)
	}
	// This edit happens before the watcher establishes its baseline.
	write("business:\n  message: before-watch\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events := make(chan error, 8)
	done := make(chan error, 1)
	go func() {
		done <- reload.Watch(ctx, reload.WatchOptions{Paths: in.Paths(), Interval: 10 * time.Millisecond, Debounce: 20 * time.Millisecond, OnChange: func(ctx context.Context) error { _, err := reloadSettings(ctx, in, m); events <- err; return err }})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("watcher failed to exit")
		}
	}()
	wait := func(wantError bool) {
		t.Helper()
		select {
		case err := <-events:
			if (err != nil) != wantError {
				t.Fatalf("unexpected reload outcome: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("reload event timeout")
		}
	}
	wait(false)
	if m.Snapshot().Value.Business.Message != "before-watch" {
		t.Fatal("startup change missed")
	}
	write("business:\n  message: [broken\n")
	wait(true)
	if m.Snapshot().Version != 2 {
		t.Fatal("invalid update changed revision")
	}
	write("business:\n  message: recovered\n")
	wait(false)
	if s := m.Snapshot(); s.Version != 3 || s.Value.Business.Message != "recovered" {
		t.Fatal("failed to recover")
	}
}
