package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	appconfig "github.com/maxtanforge/go-frame/app/config"
)

func TestValidateSettingsRequiresDatabaseAndIdentity(t *testing.T) {
	valid := Settings{Config: appconfig.Defaults(), Host: "127.0.0.1", Verifier: testVerifier{}}
	valid.Config.Database.URL = "postgres://user:private-secret@localhost/example?sslmode=disable"
	valid.Config.ID.Node = 0
	if err := validateSettings(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Settings){
		func(s *Settings) { s.Config.Database.Driver = "unsupported" },
		func(s *Settings) { s.Config.Database.URL = "" },
		func(s *Settings) { s.Config.ID.Node = -1 },
		func(s *Settings) { s.Host = "invalid host" },
		func(s *Settings) { s.Verifier = nil },
	} {
		s := valid
		change(&s)
		if err := validateSettings(s); err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatalf("expected safe configuration rejection, got %v", err)
		}
	}
}

func TestLoadRejectsMissingAuthAndRunRejectsCancelledContext(t *testing.T) {
	_, err := LoadFrom(func(key string) (string, bool) {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:private-secret@localhost/example", true
		case "ID_NODE":
			return "0", true
		default:
			return "", false
		}
	})
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("expected safe auth rejection, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Run: %v", err)
	}
}
