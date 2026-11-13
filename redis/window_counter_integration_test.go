package redis

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWindowCounterIntegration(t *testing.T) {
	addr := os.Getenv("FRAME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set FRAME_TEST_REDIS_ADDR to opt in to actual Redis Lua tests")
	}
	options := Options{Addr: addr, Username: os.Getenv("FRAME_TEST_REDIS_USERNAME"), Password: os.Getenv("FRAME_TEST_REDIS_PASSWORD")}
	if raw := os.Getenv("FRAME_TEST_REDIS_DB"); raw != "" {
		db, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.GreaterOrEqual(t, db, 0)
		options.DB = db
	}
	pool, err := Open(options)
	require.NoError(t, err)
	// Cleanup runs before Close and uses only this test's unique keys.
	t.Cleanup(func() { _ = pool.Close() })
	namespace := "go-frame-window-" + rand.Text()
	counter, err := NewWindowCounter(pool.Client, CounterOptions{Namespace: namespace})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, pool.Ping(ctx))
	var keys []string
	key := func(name string) string {
		physical := namespace + ":" + name
		keys = append(keys, physical)
		return physical
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if len(keys) > 0 {
			require.NoError(t, pool.Client.Del(cleanup, keys...).Err())
		}
	})

	t.Run("fixed-window-and-reset", func(t *testing.T) {
		physical := key("fixed")
		first, err := counter.Increment(ctx, "fixed", time.Second)
		require.NoError(t, err)
		require.Equal(t, int64(1), first.Count)
		require.Greater(t, first.TTL, time.Duration(0))
		require.LessOrEqual(t, first.TTL, time.Second)
		time.Sleep(30 * time.Millisecond)
		second, err := counter.Increment(ctx, "fixed", time.Minute)
		require.NoError(t, err)
		require.Equal(t, int64(2), second.Count)
		require.Less(t, second.TTL, first.TTL)
		// A shorter caller window also does not replace the active expiration.
		third, err := counter.Increment(ctx, "fixed", time.Millisecond)
		require.NoError(t, err)
		require.Equal(t, int64(3), third.Count)
		require.Greater(t, third.TTL, time.Millisecond)
		require.Eventually(t, func() bool {
			n, err := pool.Client.Exists(ctx, physical).Result()
			return err == nil && n == 0
		}, 3*time.Second, 20*time.Millisecond)
		reset, err := counter.Increment(ctx, "fixed", time.Second)
		require.NoError(t, err)
		require.Equal(t, int64(1), reset.Count)
	})
	t.Run("repair-and-precision", func(t *testing.T) {
		physical := key("legacy")
		// Missing-TTL fixture is always cleaned up, even if the script fails.
		require.NoError(t, pool.Client.Set(ctx, physical, "9007199254740992", 0).Err())
		result, err := counter.Increment(ctx, "legacy", time.Minute)
		require.NoError(t, err)
		require.Equal(t, int64(9007199254740993), result.Count)
		require.Greater(t, result.TTL, time.Duration(0))
		require.LessOrEqual(t, result.TTL, time.Minute)
		value, err := pool.Client.Get(ctx, physical).Result()
		require.NoError(t, err)
		require.Equal(t, "9007199254740993", value)
	})
	t.Run("namespace-isolation-and-zero", func(t *testing.T) {
		physical := key("zero")
		require.NoError(t, pool.Client.Set(ctx, physical, "0", time.Minute).Err())
		first, err := counter.Increment(ctx, "zero", time.Minute)
		require.NoError(t, err)
		require.Equal(t, int64(1), first.Count)
		other, err := NewWindowCounter(pool.Client, CounterOptions{Namespace: namespace + "-other"})
		require.NoError(t, err)
		keys = append(keys, namespace+"-other:zero")
		separate, err := other.Increment(ctx, "zero", time.Minute)
		require.NoError(t, err)
		require.Equal(t, int64(1), separate.Count)
		second, err := counter.Increment(ctx, "zero", time.Minute)
		require.NoError(t, err)
		require.Equal(t, int64(2), second.Count)
	})
	t.Run("concurrent-exact-increments", func(t *testing.T) {
		key("concurrent")
		const n = 64
		type reply struct {
			count WindowCount
			err   error
		}
		replies := make(chan reply, n)
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				count, err := counter.Increment(ctx, "concurrent", time.Minute)
				replies <- reply{count: count, err: err}
			})
		}
		wg.Wait()
		close(replies)
		seen := make(map[int64]bool)
		for result := range replies {
			require.NoError(t, result.err)
			require.Greater(t, result.count.TTL, time.Duration(0))
			require.LessOrEqual(t, result.count.TTL, time.Minute)
			require.False(t, seen[result.count.Count])
			seen[result.count.Count] = true
		}
		for i := int64(1); i <= n; i++ {
			require.True(t, seen[i], "missing count %d", i)
		}
	})
	t.Run("malformed-and-overflow-no-mutation", func(t *testing.T) {
		values := []string{"", "-1", "-0", "+1", "01", "1.0", "1e2", " 1", "9223372036854775807", "9223372036854775808", "9999999999999999999999"}
		for i, value := range values {
			name := fmt.Sprintf("invalid-%d", i)
			physical := key(name)
			// No TTL makes accidental repair before validation observable.
			require.NoError(t, pool.Client.Set(ctx, physical, value, 0).Err())
			_, err := counter.Increment(ctx, name, time.Minute)
			require.Error(t, err, "value %q", value)
			after, err := pool.Client.Get(ctx, physical).Result()
			require.NoError(t, err)
			require.Equal(t, value, after)
			ttl, err := pool.Client.PTTL(ctx, physical).Result()
			require.NoError(t, err)
			require.Equal(t, time.Duration(-1), ttl)
		}
		physical := key("last-valid")
		require.NoError(t, pool.Client.Set(ctx, physical, "9223372036854775806", time.Minute).Err())
		last, err := counter.Increment(ctx, "last-valid", time.Minute)
		require.NoError(t, err)
		require.Equal(t, int64(9223372036854775807), last.Count)
		_, err = counter.Increment(ctx, "last-valid", time.Minute)
		require.Error(t, err)
	})
	t.Run("wrong-type-no-mutation", func(t *testing.T) {
		physical := key("list")
		require.NoError(t, pool.Client.RPush(ctx, physical, "untouched").Err())
		require.NoError(t, pool.Client.Expire(ctx, physical, time.Minute).Err())
		before, err := pool.Client.PTTL(ctx, physical).Result()
		require.NoError(t, err)
		_, err = counter.Increment(ctx, "list", 2*time.Minute)
		require.Error(t, err)
		values, err := pool.Client.LRange(ctx, physical, 0, -1).Result()
		require.NoError(t, err)
		require.Equal(t, []string{"untouched"}, values)
		after, err := pool.Client.PTTL(ctx, physical).Result()
		require.NoError(t, err)
		require.Greater(t, after, time.Duration(0))
		require.LessOrEqual(t, after, before)
	})
}
