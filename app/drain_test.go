package app

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShutdownDelayValidation(t *testing.T) {
	svc := &testService{}
	for _, delay := range []time.Duration{-time.Second, 10 * time.Second, 11 * time.Second} {
		_, err := New(svc, Options{ShutdownTimeout: 10 * time.Second, ShutdownDelay: delay})
		require.Error(t, err)
	}
	_, err := New(svc, Options{ShutdownDelay: time.Second})
	require.NoError(t, err)
}

func TestWithdrawalUsesShutdownBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		exit := make(chan struct{})
		var unreadyAt, shutdownAt time.Time
		var remaining time.Duration
		forced, stopped := false, false
		svc := &testService{
			run: func() error { cancel(); <-exit; return nil },
			shutdown: func(ctx context.Context) error {
				shutdownAt = time.Now()
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				remaining = time.Until(deadline)
				<-ctx.Done()
				return ctx.Err()
			},
			close: func() error { forced = true; close(exit); return nil },
		}
		a, err := New(svc, Options{ShutdownTimeout: 10 * time.Second, ShutdownDelay: 3 * time.Second,
			OnShutdown: func() { unreadyAt = time.Now() },
			Hooks: []Hook{{Name: "dependency", Stop: func(ctx context.Context) error {
				stopped = true
				require.True(t, forced)
				require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
				return nil
			}}},
		})
		require.NoError(t, err)
		require.ErrorIs(t, a.Run(ctx), context.DeadlineExceeded)
		require.Equal(t, 3*time.Second, shutdownAt.Sub(unreadyAt))
		require.Equal(t, 7*time.Second, remaining)
		require.True(t, stopped)
	})
}

func TestServiceExitSkipsOrInterruptsWithdrawal(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("listener failed")
			start := time.Now()
			svc := &testService{
				run: func() error {
					if cancelFirst {
						cancel()
						time.Sleep(time.Second)
					}
					return failure
				},
				shutdown: func(context.Context) error { return nil },
				close:    func() error { return nil },
			}
			a, err := New(svc, Options{ShutdownTimeout: 10 * time.Second, ShutdownDelay: 5 * time.Second})
			require.NoError(t, err)
			require.ErrorIs(t, a.Run(ctx), failure)
			want := time.Duration(0)
			if cancelFirst {
				want = time.Second
			}
			require.Equal(t, want, time.Since(start))
		})
	}
}

func TestStartupFailureSkipsWithdrawal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("database unavailable")
		start := time.Now()
		a, err := New(&testService{}, Options{ShutdownDelay: time.Second, Hooks: []Hook{{
			Name: "database", Start: func(context.Context) error { return failure },
		}}})
		require.NoError(t, err)
		require.ErrorIs(t, a.Run(context.Background()), failure)
		require.Zero(t, time.Since(start))
	})
}
