// Package memory provides a bounded process-local LRU cache with lazy expiry.
package memory

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/maxtanforge/go-frame/cache"
)

// Config bounds the store: entry capacity, per-value byte limit and an
// injectable clock for tests.
type Config struct {
	Capacity int
	cache.Options
	Clock func() time.Time
}
type entry struct {
	value   []byte
	expires time.Time
}

// Cache owns its entries. A single mutex serializes expiry and LRU updates.
type Cache struct {
	mu      sync.Mutex
	entries *lru.Cache[string, entry]
	clock   func() time.Time
	limit   int
	closed  bool
}

var _ cache.Store = (*Cache)(nil)

// New builds an empty bounded store; capacity must be positive.
func New(c Config) (*Cache, error) {
	if c.Capacity <= 0 {
		return nil, errors.New("cache/memory: capacity must be positive")
	}
	limit, e := c.Limit()
	if e != nil {
		return nil, e
	}
	entries, e := lru.New[string, entry](c.Capacity)
	if e != nil {
		return nil, e
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	return &Cache{entries: entries, clock: c.Clock, limit: limit}, nil
}
func (c *Cache) check(ctx context.Context, key string) error {
	if c.closed {
		return cache.ErrClosed
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	return cache.ValidateKey(key)
}

// Get returns a copy of a live entry; expired entries count as missing.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.check(ctx, key); e != nil {
		return nil, false, e
	}
	v, ok := c.entries.Get(key)
	if !ok {
		return nil, false, nil
	}
	if !v.expires.IsZero() && !c.clock().Before(v.expires) {
		c.entries.Remove(key)
		return nil, false, nil
	}
	return bytes.Clone(v.value), true, nil
}

// Set stores a private copy with a whole-millisecond TTL; zero TTL means no expiry.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.check(ctx, key); e != nil {
		return e
	}
	if e := cache.ValidateTTL(ttl); e != nil {
		return e
	}
	if len(value) > c.limit {
		return cache.ErrValueTooLarge
	}
	v := entry{value: bytes.Clone(value)}
	if ttl > 0 {
		v.expires = c.clock().Add(ttl)
	}
	c.entries.Add(key, v)
	return nil
}

// Delete removes the key; deleting a missing key is not an error.
func (c *Cache) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.check(ctx, key); e != nil {
		return e
	}
	c.entries.Remove(key)
	return nil
}

// Close clears all entries. Subsequent operations return cache.ErrClosed.
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.entries.Purge()
	}
	return nil
}
