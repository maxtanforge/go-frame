package request_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/request"
	"github.com/stretchr/testify/require"
)

func TestClientIPTrustBoundaryAndMalformedHeaders(t *testing.T) {
	resolver, err := request.NewClientIPResolver("172.17.0.1, 10.0.0.0/24, ::ffff:192.0.2.0/120, 2001:db8:1::/64")
	require.NoError(t, err)
	for _, tc := range []struct{ name, peer, forwarded, real, want string }{
		{"direct", "198.51.100.2:123", "203.0.113.1", "", "198.51.100.2"},
		{"trusted chain", "172.17.0.1:123", "198.51.100.1, 10.0.0.2", "", "198.51.100.1"},
		{"spoofed left hop", "172.17.0.1:123", "203.0.113.1, 198.51.100.1", "", "198.51.100.1"},
		{"invalid", "172.17.0.1:123", "invalid, 10.0.0.2", "203.0.113.1", "172.17.0.1"},
		{"oversized", "172.17.0.1:123", strings.Repeat("x", 4097), "", "172.17.0.1"},
		{"too many hops", "172.17.0.1:123", strings.Repeat("10.0.0.2,", 64) + "198.51.100.1", "", "172.17.0.1"},
		{"mapped peer", "[::ffff:192.0.2.1]:123", "198.51.100.1", "", "198.51.100.1"},
		{"IPv6", "[2001:db8:1::1]:123", "2001:db8:2::1", "", "2001:db8:2::1"},
		{"zoned header", "172.17.0.1:123", "fe80::1%eth0", "", "172.17.0.1"},
		{"real IP", "172.17.0.1:123", "", "198.51.100.1", "198.51.100.1"},
		{"invalid peer", "invalid", "198.51.100.1", "", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.peer
			req.Header.Set("X-Forwarded-For", tc.forwarded)
			req.Header.Set("X-Real-IP", tc.real)
			require.Equal(t, tc.want, resolver.Resolve(req))
			resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				r.Header.Set("X-Forwarded-For", "203.0.113.99")
				require.Equal(t, tc.want, request.ClientIP(r))
			})).ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

func TestClientIPMultipleHeadersAndDefaultTrust(t *testing.T) {
	resolver, err := request.NewClientIPResolver("10.0.0.0/24")
	require.NoError(t, err)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:123"
	r.Header.Add("X-Forwarded-For", "198.51.100.1")
	r.Header.Add("X-Forwarded-For", "10.0.0.2")
	require.Equal(t, "198.51.100.1", resolver.Resolve(r))
	resolver, err = request.NewClientIPResolver("")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", resolver.Resolve(r))
	require.Equal(t, "10.0.0.1", request.ClientIP(r))
	for _, invalid := range []string{"invalid", "10.0.0.1,", "fe80::1%eth0", "::ffff:192.0.2.1/80", strings.Repeat("10.0.0.1,", 64) + "10.0.0.1", strings.Repeat("x", 4097)} {
		_, err := request.NewClientIPResolver(invalid)
		require.Error(t, err)
	}
}
