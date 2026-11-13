// Package cache defines bounded byte stores and explicit JSON serialization helpers.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// DefaultMaxValueBytes bounds one cached value (1 MiB) unless Options overrides it.
const DefaultMaxValueBytes = 1 << 20

// MaxKeyBytes is the hard key-length ceiling shared by every store.
const MaxKeyBytes = 512

// Sentinel errors returned by every Store implementation.
var (
	// ErrInvalidKey rejects empty or over-long keys.
	ErrInvalidKey = errors.New("cache: key must contain 1 to 512 bytes")
	// ErrInvalidTTL rejects fractional or negative TTLs.
	ErrInvalidTTL = errors.New("cache: TTL must be zero or positive whole milliseconds")
	// ErrValueTooLarge rejects values beyond the configured byte bound.
	ErrValueTooLarge = errors.New("cache: value too large")
	// ErrClosed reports use after Close.
	ErrClosed = errors.New("cache: closed")
)

// Store distinguishes a missing key from a present empty value. Implementations
// must honor cancellation and must not silently fall back to another source.
type Store interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
}

// Options bounds each value. Zero selects DefaultMaxValueBytes.
type Options struct{ MaxValueBytes int }

// Limit resolves the effective byte bound; negative values are rejected.
func (o Options) Limit() (int, error) {
	if o.MaxValueBytes < 0 {
		return 0, errors.New("cache: negative value limit")
	}
	if o.MaxValueBytes == 0 {
		return DefaultMaxValueBytes, nil
	}
	return o.MaxValueBytes, nil
}

// ValidateKey accepts 1..MaxKeyBytes arbitrary bytes; keys are not parsed.
func ValidateKey(key string) error {
	if len(key) == 0 || len(key) > MaxKeyBytes {
		return ErrInvalidKey
	}
	return nil
}

// ValidateTTL accepts zero (no expiry) or positive whole milliseconds.
func ValidateTTL(ttl time.Duration) error {
	if ttl < 0 || ttl%time.Millisecond != 0 {
		return ErrInvalidTTL
	}
	return nil
}
func jsonLimit(options []Options) (int, error) {
	if len(options) > 1 {
		return 0, errors.New("cache: at most one Options value is allowed")
	}
	if len(options) == 1 {
		return options[0].Limit()
	}
	return DefaultMaxValueBytes, nil
}

// GetJSON preserves the hit flag even when a present value cannot be decoded.
// JSON null is a hit; use a pointer type when null must differ from the zero value.
func GetJSON[T any](ctx context.Context, s Store, key string, options ...Options) (T, bool, error) {
	var v T
	if e := ctx.Err(); e != nil {
		return v, false, e
	}
	if e := ValidateKey(key); e != nil {
		return v, false, e
	}
	limit, e := jsonLimit(options)
	if e != nil {
		return v, false, e
	}
	b, ok, e := s.Get(ctx, key)
	if e != nil || !ok {
		return v, ok, e
	}
	if len(b) > limit {
		return v, true, ErrValueTooLarge
	}
	e = json.Unmarshal(b, &v)
	if e != nil {
		var zero T
		return zero, true, e
	}
	return v, true, nil
}

// SetJSON serializes a value and rejects oversized output before calling Store.
// The bound applies to serialized bytes, not transient json.Marshal allocations.
func SetJSON[T any](ctx context.Context, s Store, key string, v T, ttl time.Duration, options ...Options) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := ValidateKey(key); e != nil {
		return e
	}
	if e := ValidateTTL(ttl); e != nil {
		return e
	}
	limit, e := jsonLimit(options)
	if e != nil {
		return e
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > limit {
		return ErrValueTooLarge
	}
	return s.Set(ctx, key, b, ttl)
}
