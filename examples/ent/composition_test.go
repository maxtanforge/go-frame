package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/auth/token"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/metric"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedProductComposition(t *testing.T) {
	pool, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, pool.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	client, err := newClient(pool, "mysql", entdb.Options{}, entdb.MutationOptions{IDs: fixedIDs{}, Now: func() time.Time { return testNow }})
	require.NoError(t, err)
	service, err := NewService(client)
	require.NoError(t, err)
	var events []operation.Event
	var observations []metric.Observation
	metrics, err := metric.NewRequestRecorder(metric.Options{OnObserve: func(ctx context.Context, o metric.Observation) {
		require.NotNil(t, ctx)
		observations = append(observations, o)
	}})
	require.NoError(t, err)
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	verifier := auth.VerifyFunc(func(_ context.Context, raw string) (principal.Principal, error) {
		if raw != "test-credential" {
			return principal.Principal{}, principal.ErrUnauthenticated
		}
		return principal.Principal{ID: "operator-42", Name: "Alice", Issuer: "test-issuer"}, nil
	})
	routes, err := newRoutes(lg, router.Options{}, verifier, operation.SinkFunc(func(_ context.Context, e operation.Event) error {
		events = append(events, e)
		return nil
	}), metrics)
	require.NoError(t, err)
	require.NoError(t, Register(routes, service))
	routes.Get("/later", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	request := func(method, path, credential, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, req)
		return w
	}
	for _, credential := range []string{"", "invalid"} {
		require.Equal(t, http.StatusUnauthorized, request("POST", "/products", credential, `{"name":"tea"}`).Code)
	}
	require.Equal(t, http.StatusBadRequest, request("POST", "/products", "test-credential", `{"name":"","created_by":"forged"}`).Code)
	require.NoError(t, mock.ExpectationsWereMet(), "rejected requests must not begin a transaction")
	mock.ExpectBegin()
	mock.ExpectExec(exactSQL("INSERT INTO `products` (`created_at`, `updated_at`, `created_by`, `updated_by`, `created_by_kind`, `updated_by_kind`, `created_by_name`, `updated_by_name`, `name`, `price`, `active`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")).
		WithArgs(testNow, testNow, "operator-42", "operator-42", "subject", "subject", "Alice", "Alice", "tea", int64(199), true, int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(exactSQL("INSERT INTO `product_events` (`created_at`, `created_by`, `created_by_kind`, `created_by_name`, `product_id`, `action`, `id`) VALUES (?, ?, ?, ?, ?, ?, ?)")).
		WithArgs(testNow, "operator-42", "subject", "Alice", int64(123), "created", int64(123)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	w := request("POST", "/products", "test-credential", `{"name":"tea","price":199,"active":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"created_by":"operator-42"`)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, events, 4)
	require.Equal(t, operation.Denied, events[0].Outcome)
	require.Empty(t, events[0].ActorID)
	require.Equal(t, operation.Completed, events[3].Outcome)
	require.Equal(t, "operator-42", events[3].ActorID)
	require.Equal(t, "Alice", events[3].ActorName)
	require.Contains(t, w.Body.String(), `"created_by_name":"Alice"`)
	require.Equal(t, "test-issuer", events[3].ActorIssuer)
	require.Equal(t, "/products", events[3].Route)
	require.Len(t, observations, 4)
	require.Equal(t, metric.RequestKey{Route: "/products", Method: "POST"}, metric.RequestKey{Route: observations[3].Route, Method: observations[3].Method})
	require.GreaterOrEqual(t, observations[3].DurationMS, float64(0))
	// Health is the only declared public surface; even newly added routes require identity.
	require.Equal(t, http.StatusServiceUnavailable, request("GET", "/healthz", "invalid", "").Code)
	require.Equal(t, http.StatusUnauthorized, request("GET", "/later", "", "").Code)
	require.Equal(t, http.StatusUnauthorized, request("GET", "/metrics", "", "").Code)
	exported := request("GET", "/metrics", "test-credential", "")
	require.Equal(t, http.StatusOK, exported.Code)
	require.Equal(t, "text/plain; version=0.0.4", exported.Header().Get("Content-Type"))
	require.Contains(t, exported.Body.String(), `http_request_duration_milliseconds_count{method="POST",route="/products"} 2`)
	require.Equal(t, uint64(1), metrics.Snapshot()[metric.RequestKey{Route: "/metrics", Method: "GET"}].Count)
	require.Len(t, routes.Policies(), 5)
	for _, policy := range routes.Policies() {
		if policy.Path == "/metrics" {
			require.False(t, policy.Public)
			continue
		}
		require.True(t, policy.Public)
		require.Contains(t, []string{"/healthz", "/readyz"}, policy.Path)
	}
}

func TestAuthSettingsRejectMissingOrMalformedKeys(t *testing.T) {
	for _, values := range []map[string]string{
		{},
		{"ENT_AUTH_ISSUER": "example", "ENT_AUTH_AUDIENCE": "products", "ENT_AUTH_SIGNING_KEY_ID": "sig", "ENT_AUTH_ENCRYPTION_KEY_ID": "enc", "ENT_AUTH_PUBLIC_KEY_FILE": "missing.pem", "ENT_AUTH_ENCRYPTION_KEY": "invalid"},
	} {
		_, err := loadVerifier(func(key string) string { return values[key] })
		require.Error(t, err)
	}
}

func TestConfiguredVerifierAuthenticatesIssuedToken(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	encryptionKey := make([]byte, 32)
	_, err = rand.Read(encryptionKey)
	require.NoError(t, err)
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	keyFile := filepath.Join(t.TempDir(), "public.pem")
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600))
	values := map[string]string{
		"ENT_AUTH_ISSUER": "test-issuer", "ENT_AUTH_AUDIENCE": "products",
		"ENT_AUTH_SIGNING_KEY_ID": "sig", "ENT_AUTH_ENCRYPTION_KEY_ID": "enc",
		"ENT_AUTH_PUBLIC_KEY_FILE": keyFile, "ENT_AUTH_ENCRYPTION_KEY": base64.RawStdEncoding.EncodeToString(encryptionKey),
	}
	verifier, err := loadVerifier(func(key string) string { return values[key] })
	require.NoError(t, err)
	issuer, err := token.NewIssuer(token.IssuerOptions{Issuer: "test-issuer", Audience: "products", SigningKeyID: "sig", EncryptionKeyID: "enc", SigningKey: key, EncryptionKey: encryptionKey})
	require.NoError(t, err)
	credential, err := issuer.Issue(context.Background(), principal.Principal{ID: "operator-42"})
	require.NoError(t, err)
	p, err := verifier.Verify(context.Background(), credential.AccessToken)
	require.NoError(t, err)
	require.Equal(t, "operator-42", p.ID)
	for _, bad := range []string{"invalid", base64.RawStdEncoding.EncodeToString(make([]byte, 32))} {
		values["ENT_AUTH_ENCRYPTION_KEY"] = bad
		_, err := loadVerifier(func(key string) string { return values[key] })
		require.Error(t, err)
	}
}

func TestApplicationRejectsInvalidAuthBeforeDatabaseStartup(t *testing.T) {
	cfg := appconfig.Config{}
	cfg.Database.URL = "must-not-open"
	cfg.Database.Driver = "mysql"
	cfg.ID.Node = 0
	err := runApplication(context.Background(), applicationSettings{Config: cfg}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.ErrorContains(t, err, "verifier is required")
}
