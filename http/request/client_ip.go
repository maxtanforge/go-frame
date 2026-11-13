package request

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPKey struct{}

// ClientIPResolver trusts forwarding headers only from explicitly configured
// peers. Its immutable configuration is safe for concurrent use.
type ClientIPResolver struct{ trusted []netip.Prefix }

// NewClientIPResolver accepts a comma-separated list of IPs/CIDRs, at most
// 4096 bytes and 64 entries. Empty configuration trusts no proxy.
func NewClientIPResolver(raw string) (*ClientIPResolver, error) {
	resolver := &ClientIPResolver{}
	if len(raw) > 4096 {
		return nil, errors.New("request: trusted proxy configuration too large")
	}
	if strings.TrimSpace(raw) == "" {
		return resolver, nil
	}
	items := strings.Split(raw, ",")
	if len(items) > 64 {
		return nil, errors.New("request: too many trusted proxies")
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			address, parseErr := netip.ParseAddr(item)
			if parseErr != nil || address.Zone() != "" {
				return nil, errors.New("request: invalid trusted proxy address")
			}
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return nil, errors.New("request: invalid mapped IPv4 proxy prefix")
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		resolver.trusted = append(resolver.trusted, prefix.Masked())
	}
	return resolver, nil
}

func peerIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	return ip.WithZone("").Unmap().String()
}

// ClientIP returns the middleware snapshot, or the direct peer if unconfigured.
// It is network metadata, not an authenticated identity.
func ClientIP(r *http.Request) string {
	if r != nil {
		if ip, ok := r.Context().Value(clientIPKey{}).(string); ok {
			return ip
		}
	}
	return peerIP(r)
}

func (resolver *ClientIPResolver) trustedIP(ip netip.Addr) bool {
	if resolver != nil {
		for _, prefix := range resolver.trusted {
			if prefix.Contains(ip.Unmap()) {
				return true
			}
		}
	}
	return false
}

// Resolve walks X-Forwarded-For from right to left to the first untrusted hop.
// Missing XFF falls back to X-Real-IP. Invalid, zoned, oversized (>4096 bytes)
// or excessive (>64 hops) headers fall back to the direct peer. It never does DNS.
func (resolver *ClientIPResolver) Resolve(r *http.Request) string {
	peer := peerIP(r)
	ip, err := netip.ParseAddr(peer)
	if err != nil || !resolver.trustedIP(ip) {
		return peer
	}
	values := r.Header.Values("X-Forwarded-For")
	size := 0
	for _, value := range values {
		size += len(value) + 1
		if size > 4097 {
			return peer
		}
	}
	raw := strings.Join(values, ",")
	if raw == "" {
		raw = r.Header.Get("X-Real-IP")
	}
	if raw == "" || len(raw) > 4096 {
		return peer
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 64 {
		return peer
	}
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		address, parseErr := netip.ParseAddr(strings.TrimSpace(part))
		if parseErr != nil || address.Zone() != "" {
			return peer
		}
		chain = append(chain, address.Unmap())
	}
	for i := len(chain) - 1; i >= 0; i-- {
		ip = chain[i]
		if !resolver.trustedIP(ip) {
			return ip.String()
		}
	}
	return ip.String()
}

// Middleware captures the resolved address before the handler changes headers.
func (resolver *ClientIPResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), clientIPKey{}, resolver.Resolve(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
