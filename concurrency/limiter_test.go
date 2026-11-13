package concurrency

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBoundedLimiterQueueFullAndRecovery(t *testing.T) {
	l, err := NewBoundedLimiter(1, 1)
	require.NoError(t, err)
	release, err := l.Acquire(context.Background())
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		r, err := l.Acquire(ctx)
		if r != nil {
			r()
		}
		done <- err
	}()
	require.Eventually(t, func() bool { return l.Stats().Waiting == 1 }, 5*time.Second, time.Millisecond)
	r, err := l.Acquire(context.Background())
	require.Nil(t, r)
	require.ErrorIs(t, err, ErrOverloaded)
	require.Equal(t, LimiterStats{Capacity: 1, MaxWaiters: 1, InUse: 1, Waiting: 1, Rejected: 1}, l.Stats())
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Zero(t, l.Stats().Waiting)
	release()
	r, err = l.Acquire(context.Background())
	require.NoError(t, err)
	r()
	require.Zero(t, l.Stats().InUse)
}

func TestBoundedLimiterZeroWaitAndDeadline(t *testing.T) {
	for _, waiters := range []int{0, 1} {
		l, err := NewBoundedLimiter(1, waiters)
		require.NoError(t, err)
		release, err := l.Acquire(context.Background())
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		r, err := l.Acquire(ctx)
		cancel()
		require.Nil(t, r)
		if waiters == 0 {
			require.ErrorIs(t, err, ErrOverloaded)
			require.EqualValues(t, 1, l.Stats().Rejected)
		} else {
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Zero(t, l.Stats().Rejected)
		}
		require.Zero(t, l.Stats().Waiting)
		release()
		r, err = l.Acquire(context.Background())
		require.NoError(t, err)
		r()
	}
}

func TestBoundedLimiterSustainedOverload(t *testing.T) {
	const capacity, waiters = 3, 8
	l, err := NewBoundedLimiter(capacity, waiters)
	require.NoError(t, err)
	var releases []func()
	for range capacity {
		r, err := l.Acquire(context.Background())
		require.NoError(t, err)
		releases = append(releases, r)
		defer r()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var queued sync.WaitGroup
	for range waiters {
		queued.Go(func() {
			r, err := l.Acquire(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			r()
		})
	}
	require.Eventually(t, func() bool { return l.Stats().Waiting == waiters }, 5*time.Second, time.Millisecond)
	var rejected sync.WaitGroup
	for range 128 {
		rejected.Go(func() {
			for range 20 {
				r, err := l.Acquire(ctx)
				if r != nil {
					r()
					t.Error("admitted beyond occupied capacity")
				}
				if !errors.Is(err, ErrOverloaded) {
					t.Errorf("expected overload, got %v", err)
				}
			}
		})
	}
	done := make(chan struct{})
	go func() { rejected.Wait(); close(done) }()
	waitSignal(t, done)
	require.Equal(t, LimiterStats{Capacity: capacity, MaxWaiters: waiters, InUse: capacity, Waiting: waiters, Rejected: 128 * 20}, l.Stats())
	for _, r := range releases {
		r()
	}
	done = make(chan struct{})
	go func() { queued.Wait(); close(done) }()
	waitSignal(t, done)
	require.Zero(t, l.Stats().InUse)
	require.Zero(t, l.Stats().Waiting)
}

func TestBoundedLimiterValidationAndCanceledPrecedence(t *testing.T) {
	for _, limits := range [][2]int{{0, 0}, {-1, 1}, {1, -1}} {
		_, err := NewBoundedLimiter(limits[0], limits[1])
		require.Error(t, err)
	}
	l, err := NewBoundedLimiter(1, 0)
	require.NoError(t, err)
	r, ok := l.TryAcquire()
	require.True(t, ok)
	defer r()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := l.Acquire(ctx)
	require.Nil(t, release)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, l.Stats().Rejected)
	release, ok = l.TryAcquire()
	require.Nil(t, release)
	require.False(t, ok)
	require.EqualValues(t, 1, l.Stats().Rejected)
	legacy, err := NewLimiter(1)
	require.NoError(t, err)
	require.Equal(t, -1, legacy.Stats().MaxWaiters)
}
