package loader

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type settings struct {
	Env     string   `config:"env" env:"APP_ENV"`
	Message string   `config:"message" env:"MESSAGE"`
	Labels  []string `config:"labels"`
	Secret  string   `config:"secret" env:"SECRET" secret:"true"`
}

func noEnv(string) (string, bool) { return "", false }
func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestSourcesAreCapturedAndFilesReread(t *testing.T) {
	dir := t.TempDir()
	y := filepath.Join(dir, "config.yaml")
	e := filepath.Join(dir, "dev.env")
	write(t, y, "message: yaml\n")
	write(t, e, "MESSAGE=dotenv\nSECRET=super-secret\n")
	defaults := settings{Env: "dev", Labels: []string{"original"}}
	env := "environment"
	args := []string{"--config", y, "--env-file", e, "--set", "message=cli", "--watch-config"}
	in, err := New(defaults, args, Options[settings]{Lookup: func(k string) (string, bool) { return env, k == "MESSAGE" }, AllowWatch: true, AllowDotenv: func(c settings) bool { return c.Env == "dev" }})
	if err != nil {
		t.Fatal(err)
	}
	defaults.Labels[0] = "mutated"
	env = "changed"
	args[5] = "message=changed"
	paths := in.Paths()
	paths[0] = "changed"
	r, err := in.Load()
	if err != nil {
		t.Fatal(err)
	}
	if r.Value.Message != "cli" || r.Value.Labels[0] != "original" || !in.Watch() {
		t.Fatalf("capture failed: %#v", r.Value)
	}
	for _, entry := range r.Entries {
		if strings.Contains(entry.Value, "super-secret") {
			t.Fatal("secret leaked")
		}
	}
	r.Value.Labels[0] = "returned mutation"
	write(t, y, "message: new-yaml\nlabels: [new-file]\n")
	r, err = in.Load()
	if err != nil {
		t.Fatal(err)
	}
	if r.Value.Message != "cli" || r.Value.Labels[0] != "new-file" {
		t.Fatal("reload failed")
	}
	write(t, y, "message: new-yaml\n")
	r, err = in.Load()
	if err != nil {
		t.Fatal(err)
	}
	if r.Value.Labels[0] != "original" {
		t.Fatal("result mutated defaults")
	}
}
func TestEnvironmentCapturedOnce(t *testing.T) {
	calls := 0
	value := "captured"
	in, err := New(settings{}, nil, Options[settings]{Lookup: func(k string) (string, bool) { calls++; return value, k == "MESSAGE" }})
	if err != nil {
		t.Fatal(err)
	}
	capturedCalls := calls
	value = "changed"
	for i := 0; i < 2; i++ {
		r, err := in.Load()
		if err != nil {
			t.Fatal(err)
		}
		if r.Value.Message != "captured" || calls != capturedCalls {
			t.Fatal("environment recaptured")
		}
	}
}
func TestDotenvRequiresExplicitFinalPolicy(t *testing.T) {
	e := filepath.Join(t.TempDir(), "dev.env")
	write(t, e, "APP_ENV=prod\n")
	for _, allow := range []func(settings) bool{nil, func(c settings) bool { return c.Env == "dev" }} {
		in, err := New(settings{Env: "dev"}, []string{"--env-file", e}, Options[settings]{Lookup: noEnv, AllowDotenv: allow})
		if err == nil {
			_, err = in.Load()
		}
		if err == nil {
			t.Fatal("unsafe dotenv accepted")
		}
	}
	in, err := New(settings{Env: "prod"}, []string{"--env-file", e, "--set", "env=dev"}, Options[settings]{Lookup: noEnv, AllowDotenv: func(c settings) bool { return c.Env == "dev" }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = in.Load(); err != nil {
		t.Fatal(err)
	}
}
func TestSafeInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"--watch-config"}, {"--set", "secret-path"}, {"--set", "message=a", "--set", "message=super-secret"}, {"--secret-path"}, {"secret-path"}, {"--config", "secret-path"}} {
		in, err := New(settings{}, args, Options[settings]{Lookup: noEnv})
		if err == nil {
			_, err = in.Load()
		}
		if err == nil {
			t.Fatalf("accepted %v", args)
		}
		if strings.Contains(err.Error(), "secret-path") || strings.Contains(err.Error(), "super-secret") {
			t.Fatal("input leaked")
		}
	}
	if _, err := New(settings{}, nil, Options[settings]{}); err == nil {
		t.Fatal("nil lookup accepted")
	}
	if _, err := New(settings{}, []string{"--watch-config"}, Options[settings]{Lookup: noEnv, AllowWatch: true}); err == nil {
		t.Fatal("watch without paths accepted")
	}
}

func TestPrecedenceAtEachLayer(t *testing.T) {
	dir := t.TempDir()
	y, e := filepath.Join(dir, "config.yaml"), filepath.Join(dir, "dev.env")
	write(t, y, "message: yaml\n")
	write(t, e, "MESSAGE=dotenv\n")
	tests := []struct {
		name         string
		args         []string
		env          bool
		want, source string
	}{
		{"defaults", nil, false, "default", "defaults"},
		{"yaml", []string{"--config", y}, false, "yaml", "yaml"},
		{"dotenv", []string{"--config", y, "--env-file", e}, false, "dotenv", "dotenv"},
		{"environment", []string{"--config", y, "--env-file", e}, true, "environment", "environment"},
		{"cli empty", []string{"--config", y, "--env-file", e, "--set", "message="}, true, "", "cli"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, err := New(settings{Env: "dev", Message: "default"}, tt.args, Options[settings]{
				Lookup:      func(k string) (string, bool) { return "environment", tt.env && k == "MESSAGE" },
				AllowDotenv: func(c settings) bool { return c.Env == "dev" },
			})
			if err != nil {
				t.Fatal(err)
			}
			r, err := in.Load()
			if err != nil {
				t.Fatal(err)
			}
			if r.Value.Message != tt.want {
				t.Fatalf("got %q, want %q", r.Value.Message, tt.want)
			}
			for _, entry := range r.Entries {
				if entry.Path == "message" && entry.Source != tt.source {
					t.Fatalf("source %q, want %q", entry.Source, tt.source)
				}
			}
		})
	}
}

func TestParseErrorWrapsCause(t *testing.T) {
	defaults := settings{Env: "dev"}
	// 未知 flag：固定文案、不回显输入（防泄漏由 TestSafeInvalidArguments 强制）。
	if _, err := New(defaults, []string{"--unknown-flag"}, Options[settings]{Lookup: noEnv}); err == nil || !strings.Contains(err.Error(), "loader: invalid configuration flags") {
		t.Fatalf("parse error = %v, want fixed message without input echo", err)
	}
	// help：ErrHelp 可经 errors.Is 识别，供调用方特判。
	if _, err := New(defaults, []string{"-h"}, Options[settings]{Lookup: noEnv}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help error = %v, want flag.ErrHelp identifiable via errors.Is", err)
	}
	if _, err := New(defaults, []string{"positional"}, Options[settings]{Lookup: noEnv}); err == nil || !strings.Contains(err.Error(), "unexpected positional") {
		t.Fatalf("positional error = %v, want unexpected positional arguments", err)
	}
}
