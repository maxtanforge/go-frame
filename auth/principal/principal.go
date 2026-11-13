// Package principal carries validated application identity, not credentials or permissions.
package principal

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrUnauthenticated means no acceptable identity or credential is available.
var ErrUnauthenticated = errors.New("principal: unauthenticated")

// Principal is an immutable-by-value identity snapshot. TokenID is available for
// explicit session checks but is not automatically logged. A name is a snapshot,
// not an authoritative current user profile. Only trusted code should publish it.
type Principal struct{ ID, Name, Issuer, TokenID string }

// Validate enforces bounded, printable, non-blank identity fields.
func (p Principal) Validate() error {
	if !valid(p.ID, 128, false) || !valid(p.Name, 128, true) || !valid(p.Issuer, 256, false) || !valid(p.TokenID, 128, true) {
		return ErrUnauthenticated
	}
	return nil
}
func valid(s string, maxLen int, empty bool) bool {
	if s == "" {
		return empty
	}
	if len(s) > maxLen || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type key struct{}

// IntoContext publishes a trusted identity value; retrieval rejects invalid values.
func IntoContext(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, key{}, p)
}

// FromContext reads a published principal; false means none was published.
func FromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	p, ok := ctx.Value(key{}).(Principal)
	if !ok || p.Validate() != nil {
		return Principal{}, false
	}
	return p, true
}

// Require returns the published principal or ErrUnauthenticated.
func Require(ctx context.Context) (Principal, error) {
	p, ok := FromContext(ctx)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	return p, nil
}
