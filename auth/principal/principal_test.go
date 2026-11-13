package principal_test

import (
	"context"
	"errors"
	"github.com/maxtanforge/go-frame/auth/principal"
	"testing"
)

func TestContextIdentity(t *testing.T) {
	if _, err := principal.Require(context.Background()); !errors.Is(err, principal.ErrUnauthenticated) {
		t.Fatal(err)
	}
	p := principal.Principal{ID: "9007199254740993", Name: "Ada", Issuer: "service", TokenID: "token-id"}
	ctx := principal.IntoContext(context.Background(), p)
	got, err := principal.Require(ctx)
	if err != nil || got != p {
		t.Fatalf("%+v %v", got, err)
	}
	got.ID = "changed"
	again, _ := principal.FromContext(ctx)
	if again.ID != p.ID {
		t.Fatal("identity mutated")
	}
	if _, ok := principal.FromContext(principal.IntoContext(ctx, principal.Principal{})); ok {
		t.Fatal("invalid identity accepted")
	}
}
