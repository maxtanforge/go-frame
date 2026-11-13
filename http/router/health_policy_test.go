package router_test

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"testing"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedHealthPolicyRequiresExplicitExposure(t *testing.T) {
	checker, err := health.New(health.Options{})
	require.NoError(t, err)
	public := []auth.Endpoint{{Method: "GET", Path: "/healthz"}, {Method: "HEAD", Path: "/healthz"}, {Method: "GET", Path: "/readyz"}, {Method: "HEAD", Path: "/readyz"}}
	for _, tc := range []struct {
		name     string
		disabled bool
		public   []auth.Endpoint
		warn     bool
		status   int
	}{
		{"protected", false, nil, true, 401},
		{"partial", false, public[:1], true, 200},
		{"public", false, public, false, 200},
		{"disabled", true, nil, false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			r, err := router.NewAuthenticated(slog.New(slog.NewTextHandler(&logs, nil)), router.AuthenticatedOptions{Verifier: stubVerifier{}, Router: router.Options{Health: checker, DisableHealthMount: tc.disabled}, PublicEndpoints: tc.public})
			require.NoError(t, err)
			require.Equal(t, tc.warn, bytes.Contains(logs.Bytes(), []byte("built-in health endpoints require authentication")), logs.String())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestAuthenticatedHealthWarningUsesDefaultLogger(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	_, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{Verifier: stubVerifier{}})
	require.NoError(t, err)
	require.Contains(t, logs.String(), "built-in health endpoints require authentication")
}
