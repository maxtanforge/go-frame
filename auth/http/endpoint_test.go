package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/stretchr/testify/require"
)

func TestEndpointMiddlewarePolicyBoundary(t *testing.T) {
	guard, err := auth.New(auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{ID: "42", Issuer: "test"}, nil
	}), auth.Options{PublicEndpoints: []auth.Endpoint{{Method: "GET", Path: "/login"}}})
	require.NoError(t, err)
	for _, tc := range []struct {
		method, path, endpoint, token string
		status                        int
	}{
		{"GET", "/api/login", "/login", "", 204},
		{"POST", "/api/login", "/login", "", 401},
		{"GET", "/api/%6cogin", "/login", "", 401},
		{"GET", "/api/../login", "/login", "", 401},
		{"GET", "/login", "/", "", 401},
		{"GET", "/login", "/", "Bearer valid", 204},
	} {
		t.Run(tc.method+tc.path+tc.token, func(t *testing.T) {
			h := guard.EndpointMiddleware(auth.Endpoint{Method: "GET", Path: tc.endpoint})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tc.path, r.URL.EscapedPath())
				if tc.token != "" {
					_, err := principal.Require(r.Context())
					require.NoError(t, err)
				}
				w.WriteHeader(204)
			}))
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				r.Header.Set("Authorization", tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code)
		})
	}
}
