package loader

import (
	"path/filepath"
	"testing"

	config "github.com/maxtanforge/go-frame/config"
)

type requiredSettings struct {
	Secret string `config:"secret" env:"SECRET" secret:"true"`
	Calls  *int   `config:"-"`
}

func (s requiredSettings) Validate() error {
	*s.Calls++
	if s.Secret == "" {
		return config.Invalid("secret", config.RuleRequired)
	}
	return nil
}

func TestRequiredSettingsValidateOnlyAfterSources(t *testing.T) {
	for _, layer := range []string{"environment", "yaml", "missing"} {
		t.Run(layer, func(t *testing.T) {
			calls := 0
			var args []string
			if layer == "yaml" {
				path := filepath.Join(t.TempDir(), "config.yaml")
				write(t, path, "secret: required-secret\n")
				args = []string{"--config", path}
			}
			in, err := New(requiredSettings{Calls: &calls}, args, Options[requiredSettings]{Lookup: func(k string) (string, bool) { return "required-secret", layer == "environment" && k == "SECRET" }})
			if err != nil {
				t.Fatalf("New rejected incomplete defaults: %v", err)
			}
			if calls != 0 {
				t.Fatalf("New called validator %d times", calls)
			}
			for i := 1; i <= 2; i++ {
				r, err := in.Load()
				if (err != nil) != (layer == "missing") {
					t.Fatalf("Load error: %v", err)
				}
				if calls != i {
					t.Fatalf("validator calls = %d, want %d", calls, i)
				}
				if err == nil && r.Entries[0].Value != "[REDACTED]" {
					t.Fatal("secret not redacted")
				}
			}
		})
	}
}

func TestNewRejectsSchemaBeforeEnvironmentCapture(t *testing.T) {
	calls := 0
	_, err := New(struct{ Bad map[string]string }{}, nil, Options[struct{ Bad map[string]string }]{Lookup: func(string) (string, bool) { calls++; return "", false }})
	if err == nil || calls != 0 {
		t.Fatalf("error = %v, lookup calls = %d", err, calls)
	}
}
