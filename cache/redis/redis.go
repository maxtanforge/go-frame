// Package redis implements cache.Store using a borrowed Redis client.
package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maxtanforge/go-frame/cache"
	"github.com/maxtanforge/go-frame/internal/isnil"
	driver "github.com/redis/go-redis/v9"
)

// Client is the borrowed subset implemented by go-redis Client and ClusterClient.
// The adapter never closes the client.
type Client interface {
	Eval(context.Context, string, []string, ...interface{}) *driver.Cmd
	Set(context.Context, string, interface{}, time.Duration) *driver.StatusCmd
	Del(context.Context, ...string) *driver.IntCmd
}

// Config selects the key namespace and per-value byte bound.
type Config struct {
	Namespace string
	cache.Options
}

// Cache is a namespaced Redis Store; the client stays owned by the caller.
type Cache struct {
	client    Client
	namespace string
	limit     int
}

var _ cache.Store = (*Cache)(nil)

// Returning integer zero signals an existing oversized value. Missing values
// become redis.Nil. Redis executes this check and read atomically.
const boundedGet = `local n = redis.call('STRLEN', KEYS[1])
if n > tonumber(ARGV[1]) then return 0 end
return redis.call('GET', KEYS[1])`

// New validates the namespace and borrows the client without connecting.
func New(client Client, cfg Config) (*Cache, error) {
	if isnil.Check(client) {
		return nil, errors.New("cache/redis: nil client")
	}
	if cfg.Namespace == "" || strings.TrimSpace(cfg.Namespace) != cfg.Namespace || len(cfg.Namespace) > cache.MaxKeyBytes {
		return nil, errors.New("cache/redis: namespace must contain 1 to 512 bytes without surrounding whitespace")
	}
	limit, e := cfg.Limit()
	if e != nil {
		return nil, e
	}
	return &Cache{client: client, namespace: cfg.Namespace + ":", limit: limit}, nil
}
func (c *Cache) key(ctx context.Context, key string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if e := cache.ValidateKey(key); e != nil {
		return "", e
	}
	return c.namespace + key, nil
}

// Get reads through an atomic size-checked Lua read; misses surface redis.Nil.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	k, e := c.key(ctx, key)
	if e != nil {
		return nil, false, e
	}
	result, e := c.client.Eval(ctx, boundedGet, []string{k}, c.limit).Result()
	if errors.Is(e, driver.Nil) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	if n, ok := result.(int64); ok && n == 0 {
		return nil, true, cache.ErrValueTooLarge
	}
	var b []byte
	switch v := result.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = append([]byte(nil), v...)
	default:
		return nil, false, fmt.Errorf("cache/redis: unexpected bounded GET result %T", result)
	}
	if len(b) > c.limit {
		return nil, true, cache.ErrValueTooLarge
	}
	return b, true, nil
}

// Set stores the raw bytes with a TTL; oversized values are rejected locally.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	k, e := c.key(ctx, key)
	if e != nil {
		return e
	}
	if e = cache.ValidateTTL(ttl); e != nil {
		return e
	}
	if len(value) > c.limit {
		return cache.ErrValueTooLarge
	}
	return c.client.Set(ctx, k, value, ttl).Err()
}

// Delete removes the key; missing keys are not an error.
func (c *Cache) Delete(ctx context.Context, key string) error {
	k, e := c.key(ctx, key)
	if e != nil {
		return e
	}
	return c.client.Del(ctx, k).Err()
}
