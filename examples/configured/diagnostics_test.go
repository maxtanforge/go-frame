package main

import (
	"errors"
	"strings"
	"testing"

	config "github.com/maxtanforge/go-frame/config"
)

func TestConfiguredFieldDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		path, value string
		rule        config.Rule
	}{
		{"host", "raw-secret", config.RuleFormat},
		{"business.message", strings.Repeat("s", 1025), config.RuleLength},
		{"framework.log.level", "raw-secret", config.RuleChoice},
		{"framework.redis.pool_size", "0", config.RuleRange},
	} {
		t.Run(tc.path, func(t *testing.T) {
			_, err := loadSettings([]string{"--set", tc.path + "=" + tc.value}, noEnv)
			var detail *config.FieldError
			if !errors.As(err, &detail) || detail.Path != tc.path || detail.Source != "cli" || detail.Rule != tc.rule {
				t.Fatalf("missing diagnostic: %v", err)
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Fatal("input leaked")
			}
		})
	}
}
