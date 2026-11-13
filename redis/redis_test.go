package redis

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenValidation(t *testing.T) {
	for _, o := range []Options{{}, {Addr: "bad"}, {Addr: "localhost:6379", DB: -1}, {Addr: "localhost:6379", PoolSize: -1}, {Addr: "localhost:6379", ReadTimeout: -time.Second}} {
		_, err := Open(o)
		require.Error(t, err)
	}
}
func TestOpenOptionsAndClose(t *testing.T) {
	tlsConfig := &tls.Config{ServerName: "cache.example"}
	c, err := Open(Options{Addr: "localhost:6379", Username: "user", Password: "secret", DB: 3, TLSConfig: tlsConfig, PoolSize: 8, MaxIdleConns: 4, ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, PoolTimeout: 3 * time.Second, DialTimeout: 4 * time.Second})
	require.NoError(t, err)
	o := c.Client.Options()
	require.Equal(t, "user", o.Username)
	require.Equal(t, "secret", o.Password)
	require.Equal(t, 3, o.DB)
	require.True(t, o.ContextTimeoutEnabled)
	require.NotSame(t, tlsConfig, o.TLSConfig)
	tlsConfig.ServerName = "changed"
	require.Equal(t, "cache.example", o.TLSConfig.ServerName)
	require.Equal(t, 8, o.PoolSize)
	require.Zero(t, o.MinIdleConns)
	require.Equal(t, 4, o.MaxIdleConns)
	require.Equal(t, time.Second, o.ReadTimeout)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
}
func TestNilCache(t *testing.T) {
	var c *Pool
	require.Error(t, c.Ping(context.Background()))
	require.NoError(t, c.Close())
	require.Error(t, (&Pool{}).Ping(context.Background()))
}
func TestRedisIntegration(t *testing.T) {
	addr := os.Getenv("FRAME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set FRAME_TEST_REDIS_ADDR to opt in")
	}
	options := Options{Addr: addr, Username: os.Getenv("FRAME_TEST_REDIS_USERNAME"), Password: os.Getenv("FRAME_TEST_REDIS_PASSWORD")}
	if raw := os.Getenv("FRAME_TEST_REDIS_DB"); raw != "" {
		db, err := strconv.Atoi(raw)
		if err != nil || db < 0 {
			t.Fatal("FRAME_TEST_REDIS_DB must be a nonnegative integer")
		}
		options.DB = db
	}
	c, err := Open(options)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Ping(ctx))
	key := "go-frame-integration:" + rand.Text()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		// A separate client makes cleanup independent of the test client's lifetime.
		cleanup, cleanupErr := Open(options)
		if cleanupErr != nil {
			t.Error(cleanupErr)
			return
		}
		defer func() { _ = cleanup.Close() }()
		if err := cleanup.Client.Del(cleanupCtx, key).Err(); err != nil {
			t.Error(err)
		}
	})
	require.NoError(t, c.Client.Set(ctx, key, "value", time.Minute).Err())
	value, err := c.Client.Get(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, "value", value)
	ttl, err := c.Client.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, time.Minute)
	canceled, stop := context.WithCancel(context.Background())
	stop()
	require.ErrorIs(t, c.Client.Get(canceled, key).Err(), context.Canceled)
	deleted, err := c.Client.Del(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	exists, err := c.Client.Exists(ctx, key).Result()
	require.NoError(t, err)
	require.Zero(t, exists)
}

func TestDefaultPoolIsBounded(t *testing.T) {
	c, err := Open(Options{Addr: "localhost:6379"})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	o := c.Client.Options()
	require.Equal(t, 10, o.PoolSize)
	require.Equal(t, 10, o.MaxActiveConns)
	require.Equal(t, 5*time.Second, o.DialTimeout)
	require.Equal(t, 3*time.Second, o.ReadTimeout)
	require.Equal(t, 3*time.Second, o.WriteTimeout)
	require.Equal(t, 4*time.Second, o.PoolTimeout)
}
