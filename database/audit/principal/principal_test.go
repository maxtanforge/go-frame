package principal_test

import (
	"context"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/database/audit"
	auditprincipal "github.com/maxtanforge/go-frame/database/audit/principal"
	"testing"
)

func TestActorMapping(t *testing.T) {
	if _, err := auditprincipal.FromContext(context.Background()); err == nil {
		t.Fatal("missing actor accepted")
	}
	ctx := principal.IntoContext(context.Background(), principal.Principal{ID: "42", Name: "Alice", Issuer: "test"})
	actor, err := auditprincipal.FromContext(ctx)
	if err != nil || actor.Kind != audit.Subject || actor.ID != "42" || actor.Name != "Alice" {
		t.Fatalf("%+v %v", actor, err)
	}
}
