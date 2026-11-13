package id

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSequenceExhaustionCanBeCanceled(t *testing.T) {
	sf, err := NewSnowflake(1)
	require.NoError(t, err)
	sf.last = 100
	sf.seq = maxSeq
	sf.now = func() time.Time { return time.UnixMilli(epoch + 100) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err = sf.NextContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, maxSeq, sf.seq, "exhaustion must not wrap and permit duplicate IDs")
}

func TestSequenceExhaustionDetectsRollbackAndRange(t *testing.T) {
	sf, err := NewSnowflake(1)
	require.NoError(t, err)
	sf.last = 100
	sf.seq = maxSeq
	calls := 0
	sf.now = func() time.Time {
		calls++
		if calls == 1 {
			return time.UnixMilli(epoch + 100)
		}
		return time.UnixMilli(epoch + 99)
	}
	_, err = sf.NextContext(context.Background())
	require.True(t, errors.Is(err, ErrClockBackwards))
	sf.last = 0
	sf.now = func() time.Time { return time.UnixMilli(epoch + (1 << 41)) }
	_, err = sf.Next()
	require.ErrorIs(t, err, ErrTimeRange)
}

func TestNewSnowflakeNodeRange(t *testing.T) {
	_, err := NewSnowflake(0)
	require.NoError(t, err)
	_, err = NewSnowflake(1023)
	require.NoError(t, err)

	for _, bad := range []int64{-1, 1024, 4096} {
		_, err := NewSnowflake(bad)
		require.Error(t, err, "node=%d 应越界报错", bad)
	}
}

func TestNextLayoutBits(t *testing.T) {
	sf, err := NewSnowflake(123)
	require.NoError(t, err)
	v, err := sf.Next()
	require.NoError(t, err)

	assert.Equal(t, int64(0), v>>63, "符号位恒 0")
	assert.Equal(t, int64(123), (v>>12)&0x3FF, "节点位必须是 123")
}

func TestNextMonotonicIncreasing(t *testing.T) {
	sf, err := NewSnowflake(1)
	require.NoError(t, err)
	prev, err := sf.Next()
	require.NoError(t, err)
	for i := 0; i < 10000; i++ {
		cur, err := sf.Next()
		require.NoError(t, err)
		assert.Greater(t, cur, prev, "同节点雪花 ID 单调递增")
		prev = cur
	}
}

func TestNextConcurrentUnique(t *testing.T) {
	sf, err := NewSnowflake(7)
	require.NoError(t, err)

	const n = 1000
	ids := make(chan int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := sf.Next(); err == nil {
				ids <- v
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[int64]struct{})
	for v := range ids {
		_, dup := seen[v]
		require.False(t, dup, "并发下 ID 重复: %d", v)
		seen[v] = struct{}{}
	}
	assert.Len(t, seen, n, "全部 1000 个并发 ID 唯一")
}

func TestNextClockBackwardErrors(t *testing.T) {
	sf, err := NewSnowflake(1)
	require.NoError(t, err)

	base := time.Now()
	clock := base
	sf.now = func() time.Time { return clock }

	_, err = sf.Next()
	require.NoError(t, err)

	clock = base.Add(-5 * time.Millisecond)
	_, err = sf.Next()
	require.Error(t, err, "时钟回拨必须报错,不得生成回退/重复 ID")
}
