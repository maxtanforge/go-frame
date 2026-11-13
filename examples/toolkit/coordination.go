// Package toolkit illustrates direct assembly of cross-cutting foundations:
// bounded caching, Redis lease locks, admission limiting, process-local
// scheduling, the outbound HTTP client, and hand-rolled transport middleware.
// These helpers are example business code, not additional framework contracts.
package toolkit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maxtanforge/go-frame/cache"
	"github.com/maxtanforge/go-frame/cache/memory"
	rediscache "github.com/maxtanforge/go-frame/cache/redis"
	"github.com/maxtanforge/go-frame/concurrency"
	lockredis "github.com/maxtanforge/go-frame/lock/redis"
)

// NewMemoryCache builds the process-local bounded store. Capacity is entries.
func NewMemoryCache(capacity int) (*memory.Cache, error) {
	return memory.New(memory.Config{Capacity: capacity})
}

// NewRedisCache borrows an existing Redis client; ownership stays with the caller.
func NewRedisCache(client rediscache.Client) (*rediscache.Cache, error) {
	return rediscache.New(client, rediscache.Config{Namespace: "toolkit"})
}

// Cached reads through any Store: present values return without loading,
// misses load once and repopulate. Load errors are returned, never cached.
func Cached(ctx context.Context, store cache.Store, key string, ttl time.Duration, load func(context.Context) ([]byte, error)) ([]byte, error) {
	value, ok, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if ok {
		return value, nil
	}
	value, err = load(ctx)
	if err != nil {
		return nil, err
	}
	if err = store.Set(ctx, key, value, ttl); err != nil {
		return nil, err
	}
	return value, nil
}

// NewRedisLocker borrows an existing Redis client for mutual exclusion across
// replicas. Durations are integer milliseconds per the lock contract.
func NewRedisLocker(client lockredis.Client) (*lockredis.Locker, error) {
	return lockredis.New(client, lockredis.Config{
		Namespace:        "toolkit",
		TTLMS:            30_000,
		CommandTimeoutMS: 2_000,
		PollIntervalMS:   500,
		RenewIntervalMS:  10_000,
	})
}

// WithLease runs work while holding the lease. Lost ownership cancels the
// work context (ErrLost); release always runs, and both errors are returned.
func WithLease(ctx context.Context, locker *lockredis.Locker, key string, work func(context.Context) error) (err error) {
	lease, err := locker.Acquire(ctx, key, false)
	if err != nil {
		return fmt.Errorf("acquire %s: %w", key, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		err = errors.Join(err, lease.Release(cleanup))
	}()
	return work(lease.Context())
}

// NewLimiter bounds active and waiting admissions within this process.
// Exhausting both limits rejects immediately; callers bound wait time with context.
func NewLimiter(capacity int) (*concurrency.Limiter, error) {
	return concurrency.NewBoundedLimiter(capacity, capacity)
}
