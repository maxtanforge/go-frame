package redis

import (
	"context"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestWindowCounterConstructor(t *testing.T) {
	_, err := NewWindowCounter(nil, CounterOptions{Namespace: "requests"})
	require.Error(t, err)
	for _, retries := range []int{0, 1} {
		client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: retries})
		_, err := NewWindowCounter(client, CounterOptions{Namespace: "requests"})
		require.Error(t, err)
		require.NoError(t, client.Close())
	}
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	noDeadline := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = noDeadline.Close() })
	_, err = NewWindowCounter(noDeadline, CounterOptions{Namespace: "requests"})
	require.Error(t, err)
	for _, namespace := range []string{"", "requests:login", "with space", "bad\x00", strings.Repeat("n", 129)} {
		_, err := NewWindowCounter(client, CounterOptions{Namespace: namespace})
		require.Error(t, err, namespace)
	}
	for _, namespace := range []string{"requests", "requests.v1_a-b", strings.Repeat("n", 128)} {
		counter, err := NewWindowCounter(client, CounterOptions{Namespace: namespace})
		require.NoError(t, err)
		require.NotNil(t, counter)
	}
}

func TestWindowCounterValidation(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	counter, err := NewWindowCounter(client, CounterOptions{Namespace: "requests"})
	require.NoError(t, err)
	_, err = counter.Increment(nil, "login", time.Second) //nolint:staticcheck // Exercise the explicit nil-context rejection contract.
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = counter.Increment(ctx, "login", time.Second)
	require.ErrorIs(t, err, context.Canceled)
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	_, err = counter.Increment(deadline, "login", time.Second)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	for _, key := range []string{"", "with space", "bad\x00", "\xff", strings.Repeat("k", 513)} {
		_, err = counter.Increment(context.Background(), key, time.Second)
		require.Error(t, err)
	}
	for _, window := range []time.Duration{0, -time.Second, time.Nanosecond, time.Millisecond + time.Nanosecond} {
		_, err = counter.Increment(context.Background(), "login", window)
		require.Error(t, err)
	}
	var nilCounter *WindowCounter
	_, err = nilCounter.Increment(context.Background(), "login", time.Second)
	require.Error(t, err)
	_, err = (&WindowCounter{}).Increment(context.Background(), "login", time.Second)
	require.Error(t, err)
}

func TestWindowCounterPreservesNativeError(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, ContextTimeoutEnabled: true})
	counter, err := NewWindowCounter(client, CounterOptions{Namespace: "requests"})
	require.NoError(t, err)
	require.NoError(t, client.Close())
	for _, key := range []string{"login", "用户:login", strings.Repeat("k", 512)} {
		_, err = counter.Increment(context.Background(), key, time.Millisecond)
		require.ErrorIs(t, err, goredis.ErrClosed)
	}
}

func TestWindowCounterDoesNotReplayAmbiguousWrite(t *testing.T) {
	addr, executions := lostReplyServer(t, 10)
	pool, err := Open(Options{Addr: addr})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	counter, err := NewWindowCounter(pool.Client, CounterOptions{Namespace: "requests"})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = counter.Increment(ctx, "login", time.Second)
	require.Error(t, err)
	require.Equal(t, int64(1), executions.Load())
}
