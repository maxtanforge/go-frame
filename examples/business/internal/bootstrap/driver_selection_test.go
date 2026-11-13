package bootstrap

import (
	"context"
	"testing"

	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/auth/principal"
)

type testVerifier struct{}

func (testVerifier) Verify(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, principal.ErrUnauthenticated
}

func TestValidateSettingsAllowsBusinessDriverSelection(t *testing.T) {
	for _, tc := range []struct{ driver, url string }{
		{"postgres", "postgres://user:private-secret@localhost/example"},
		{"mysql", "user:private-secret@tcp(localhost:3306)/example"},
	} {
		s := Settings{Config: appconfig.Defaults(), Host: "127.0.0.1", Verifier: testVerifier{}}
		s.Config.Database.Driver, s.Config.Database.URL, s.Config.ID.Node = tc.driver, tc.url, 0
		if err := validateSettings(s); err != nil {
			t.Fatalf("%s selection rejected: %v", tc.driver, err)
		}
	}
}
