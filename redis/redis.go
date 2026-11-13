// Package redis provides an explicitly configured Redis connection pool.
package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Options configures Redis. Zero limits/timeouts select bounded defaults.
// Command deadlines are honored; cancellation after socket I/O starts may wait
// for the deadline or configured timeout. TLSConfig is cloned at construction.
type Options struct {
	Addr, Username, Password string
	DB                       int
	// MaxRetries is the command retry count. Zero disables retries; positive
	// values opt in and can repeat non-idempotent commands after a lost reply.
	MaxRetries                                          int
	TLSConfig                                           *tls.Config
	DialTimeout, ReadTimeout, WriteTimeout, PoolTimeout time.Duration
	PoolSize, MinIdleConns, MaxIdleConns                int
}

// Pool owns a Redis client. Do not copy after first use.
type Pool struct {
	Client    *goredis.Client
	closeOnce sync.Once
	closeErr  error
}

// Open creates a strictly lazy client. MinIdleConns must be zero; use OpenReady
// in a resource startup callback for prewarming and a connectivity check.
func Open(o Options) (*Pool, error) {
	if o.MinIdleConns > 0 {
		return nil, errors.New("redis: lazy Open requires MinIdleConns=0; use OpenReady for prewarming")
	}
	return open(o)
}

// OpenReady constructs a client and checks connectivity with ctx. The caller
// owns the successful result and must Close it (normally in resource Stop).
// Failure closes the client and returns nil. Driver background prewarming uses
// its own contexts and dial timeout; ctx bounds the readiness Ping, not all
// background work. Set a startup deadline and bounded DialTimeout.
func OpenReady(ctx context.Context, o Options) (*Pool, error) {
	if ctx == nil {
		return nil, errors.New("redis: startup context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := open(o)
	if err != nil {
		return nil, err
	}
	if err := c.Ping(ctx); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return c, nil
}

func open(o Options) (*Pool, error) {
	host, port, err := net.SplitHostPort(o.Addr)
	if err != nil || strings.TrimSpace(host) == "" {
		return nil, errors.New("redis: Addr must contain host and port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, errors.New("redis: invalid address port")
	}
	if o.DB < 0 || o.MaxRetries < 0 || o.PoolSize < 0 || o.MinIdleConns < 0 || o.MaxIdleConns < 0 || o.DialTimeout < 0 || o.ReadTimeout < 0 || o.WriteTimeout < 0 || o.PoolTimeout < 0 {
		return nil, errors.New("redis: database index, retries, pool limits and timeouts must be nonnegative")
	}
	if o.PoolSize > math.MaxInt32 || o.MinIdleConns > math.MaxInt32 || o.MaxIdleConns > math.MaxInt32 {
		return nil, errors.New("redis: pool limits must not exceed 2147483647")
	}
	if o.PoolSize == 0 {
		o.PoolSize = 10
	}
	if o.MaxIdleConns == 0 {
		o.MaxIdleConns = o.PoolSize
	}
	if o.MinIdleConns > o.MaxIdleConns || o.MaxIdleConns > o.PoolSize {
		return nil, errors.New("redis: idle connection limits must fit PoolSize")
	}
	if o.DialTimeout == 0 {
		o.DialTimeout = 5 * time.Second
	}
	if o.ReadTimeout == 0 {
		o.ReadTimeout = 3 * time.Second
	}
	if o.WriteTimeout == 0 {
		o.WriteTimeout = 3 * time.Second
	}
	if o.PoolTimeout == 0 {
		o.PoolTimeout = 4 * time.Second
	}
	var tlsConfig *tls.Config
	if o.TLSConfig != nil {
		tlsConfig = o.TLSConfig.Clone()
	}
	retries := o.MaxRetries
	if retries == 0 {
		retries = -1 // go-redis uses zero for its own default of three retries.
	}
	client := goredis.NewClient(&goredis.Options{
		Addr: o.Addr, Username: o.Username, Password: o.Password, DB: o.DB,
		TLSConfig:   tlsConfig,
		DialTimeout: o.DialTimeout, ReadTimeout: o.ReadTimeout,
		WriteTimeout: o.WriteTimeout, PoolTimeout: o.PoolTimeout,
		PoolSize: o.PoolSize, MaxActiveConns: o.PoolSize,
		MinIdleConns: o.MinIdleConns, MaxIdleConns: o.MaxIdleConns,
		ContextTimeoutEnabled: true,
		MaxRetries:            retries,
	})
	return &Pool{Client: client}, nil
}

// Ping verifies readiness within ctx without forcing pool creation.
func (c *Pool) Ping(ctx context.Context) error {
	if c == nil || c.Client == nil {
		return errors.New("redis: not configured")
	}
	return c.Client.Ping(ctx).Err()
}

// Close is nil-safe and returns the same result on subsequent calls.
func (c *Pool) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		if c.Client != nil {
			c.closeErr = c.Client.Close()
		}
	})
	return c.closeErr
}
