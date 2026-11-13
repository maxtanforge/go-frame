package authenticated_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/auth/token"
	example "github.com/maxtanforge/go-frame/examples/authenticated"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEncryptedTokenToOperationAndAudit(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	aes := make([]byte, 32)
	if _, err := rand.Read(aes); err != nil {
		t.Fatal(err)
	}
	issuer, err := token.NewIssuer(token.IssuerOptions{Issuer: "example", Audience: "api", SigningKeyID: "sign-1", EncryptionKeyID: "enc-1", SigningKey: key, EncryptionKey: aes})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := token.NewVerifier(token.VerifierOptions{Issuer: "example", Audience: "api", SigningKeys: map[string]*ecdsa.PublicKey{"sign-1": &key.PublicKey}, EncryptionKeys: map[string][]byte{"enc-1": aes}})
	if err != nil {
		t.Fatal(err)
	}
	// This trusted test identity stands in for a completed business login check.
	credential, err := issuer.Issue(context.Background(), principal.Principal{ID: "9007199254740993", Name: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	var events []operation.Event
	h, err := example.NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), verifier, operation.SinkFunc(func(_ context.Context, e operation.Event) error { events = append(events, e); return nil }))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path       string
		authorized bool
		status     int
	}{{"/public/info", false, 200}, {"/me", false, 401}, {"/me", true, 200}} {
		req := httptest.NewRequest("GET", tc.path, nil)
		if tc.authorized {
			req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %d %s", tc.path, w.Code, w.Body)
		}
		if tc.authorized && !strings.Contains(w.Body.String(), `"actor_id":"9007199254740993"`) {
			t.Fatal(w.Body.String())
		}
	}
	if len(events) != 3 || events[1].Outcome != operation.Denied || events[1].ActorID != "" || events[2].ActorID != "9007199254740993" {
		t.Fatalf("%+v", events)
	}
}
