package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noEnv(string) (string, bool) { return "", false }

func TestLayeringAndProvenance(t *testing.T) {
	dir := t.TempDir()
	y := filepath.Join(dir, "config.yaml")
	e := filepath.Join(dir, "dev.env")
	if err := os.WriteFile(y, []byte("business:\n  message: file\n  labels: [a, b]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e, []byte("BUSINESS_MESSAGE=dotenv\nBUSINESS_API_KEY=top-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lookup := func(k string) (string, bool) {
		if k == "BUSINESS_MESSAGE" {
			return "env", true
		}
		return "", false
	}
	r, err := loadSettings([]string{"--config", y, "--env-file", e, "--set", "business.message=", "--set", "business.labels=[]"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if r.Value.Business.Message != "" || len(r.Value.Business.Labels) != 0 || r.Value.Business.APIKey != "top-secret" {
		t.Fatalf("incorrect layered config")
	}
	found := false
	for _, entry := range r.Entries {
		if entry.Path == "business.message" {
			found = entry.Source == "cli"
		}
		if strings.Contains(entry.Value, "top-secret") {
			t.Fatal("secret in explanation")
		}
	}
	if !found {
		t.Fatal("missing provenance")
	}
}

func TestCommandsWithoutResources(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs-must-not-exist")
	for _, verb := range []string{"check", "explain"} {
		var out bytes.Buffer
		err := execute(context.Background(), []string{"config", verb, "--set", "framework.database.url=not-a-real-dsn", "--set", "business.api_key=top-secret", "--set", "framework.log.output=file", "--set", "framework.log.dir=" + logDir}, &out, noEnv)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "top-secret") || strings.Contains(out.String(), "not-a-real-dsn") {
			t.Fatal("secret leaked")
		}
	}
	if _, err := os.Stat(logDir); !os.IsNotExist(err) {
		t.Fatalf("check/explain touched log directory: %v", err)
	}
	var out bytes.Buffer
	if err := execute(context.Background(), []string{"version"}, &out, func(string) (string, bool) { t.Fatal("version consulted env"); return "", false }); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidInput(t *testing.T) {
	for _, args := range [][]string{{"--set", "business.unknown=x"}, {"--set", "business.message=a", "--set", "business.message=b"}, {"--bad", "secret"}, {"positional"}, {"--set", "bad-secret"}, {"--set", "host=invalid"}} {
		_, err := loadSettings(args, noEnv)
		if err == nil {
			t.Fatal("invalid input accepted")
		}
		if strings.Contains(err.Error(), "bad-secret") {
			t.Fatal("error echoed value")
		}
	}
	dir := t.TempDir()
	e := filepath.Join(dir, "dev.env")
	if err := os.WriteFile(e, []byte("APP_ENV=prod"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSettings([]string{"--env-file", e}, noEnv); err == nil {
		t.Fatal("dotenv accepted for prod")
	}
}
