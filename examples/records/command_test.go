package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func absentEnv(string) (string, bool) { return "", false }

func TestRecordsDefaultsAndValidatesSnowflakeNode(t *testing.T) {
	for _, node := range []string{"", "-1", "1024", "0", "1023"} {
		args := []string{"--set", "database.url=unused"}
		if node != "" {
			args = append(args, "--set", "id.node="+node)
		}
		_, err := loadSettings(args, absentEnv)
		if (err == nil) != (node == "" || node == "0" || node == "1023") {
			t.Fatalf("node %q: %v", node, err)
		}
	}
}
func TestCommandsInspectWithoutResources(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "must-not-exist")
	for _, verb := range []string{"check", "explain"} {
		var out, diagnostic bytes.Buffer
		err := executeCommands(context.Background(), []string{"config", verb, "--set", "database.url=secret-invalid-dsn", "--set", "id.node=7", "--set", "log.output=file", "--set", "log.dir=" + logDir}, &out, &diagnostic, absentEnv)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "secret-invalid-dsn") || diagnostic.Len() != 0 {
			t.Fatal("inspection leaked credentials or logged")
		}
	}
	if _, err := os.Stat(logDir); !os.IsNotExist(err) {
		t.Fatal("inspection opened logger")
	}
	var out, diagnostic bytes.Buffer
	if err := executeCommands(context.Background(), []string{"version"}, &out, &diagnostic, func(string) (string, bool) { t.Fatal("version read environment"); return "", false }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"version"`) {
		t.Fatal("version missing")
	}
}
func TestRecordsConfigLayeringAndFinalValidation(t *testing.T) {
	dir := t.TempDir()
	y := filepath.Join(dir, "config.yaml")
	e := filepath.Join(dir, "dev.env")
	if err := os.WriteFile(y, []byte("database:\n  url: yaml-secret\nhttp:\n  port: '3001'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e, []byte("DATABASE_URL=dotenv-secret\nHTTP_PORT=3002\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := loadSettings([]string{"--config", y, "--env-file", e, "--set", "http.port=3004", "--set", "id.node=7"}, func(k string) (string, bool) { return "3003", k == "HTTP_PORT" })
	if err != nil {
		t.Fatal(err)
	}
	if r.Value.HTTP.Port != "3004" || r.Value.Database.URL != "dotenv-secret" {
		t.Fatal("precedence wrong")
	}
	if _, err := loadSettings(nil, absentEnv); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := loadSettings([]string{"--watch-config", "--config", y}, absentEnv); err == nil {
		t.Fatal("unsupported watcher accepted")
	}
	if _, err := loadSettings([]string{"--env-file", e, "--set", "service.env=prod"}, absentEnv); err == nil {
		t.Fatal("production dotenv accepted")
	}
}
func TestCommandErrorsAreReportedOnce(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	var out, diagnostic bytes.Buffer
	if err := executeCommands(context.Background(), []string{"run"}, &out, &diagnostic, absentEnv); err == nil {
		t.Fatal("run accepted missing DB")
	}
	if bytes.Count(logs.Bytes(), []byte(`"level":"ERROR"`)) != 1 || diagnostic.Len() != 0 {
		t.Fatal("run error reporting ownership changed")
	}
	logs.Reset()
	if err := executeCommands(context.Background(), []string{"config", "check"}, &out, &diagnostic, absentEnv); err == nil {
		t.Fatal("check accepted missing DB")
	}
	if logs.Len() != 0 || strings.Count(diagnostic.String(), "DATABASE_URL is required") != 1 {
		t.Fatal("inspection reporting ownership changed")
	}
}
