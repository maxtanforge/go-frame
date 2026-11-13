package app

import (
	"context"
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCleanupReserveValidation(t *testing.T) {
	for _, opts := range []Options{
		{CleanupReserve: -time.Second},
		{ShutdownTimeout: 10 * time.Second, CleanupReserve: 10 * time.Second},
		{ShutdownTimeout: 10 * time.Second, ShutdownDelay: 7 * time.Second, CleanupReserve: 3 * time.Second},
		{ShutdownTimeout: time.Duration(math.MaxInt64), ShutdownDelay: time.Duration(math.MaxInt64 - 1), CleanupReserve: 2},
	} {
		_, err := New(&testService{}, opts)
		require.Error(t, err)
	}
	_, err := New(&testService{}, Options{CleanupReserve: time.Second})
	require.NoError(t, err)
}

func TestCleanupReserveBudgets(t *testing.T) {
	for _, mode := range []string{"normal", "drain timeout", "ignored close", "early drain failure", "early exit", "exit during withdrawal"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				exit := make(chan struct{})
				failure := errors.New("service failed")
				started := time.Now()
				forced := false
				svc := &testService{
					run: func() error {
						if mode == "early exit" {
							return failure
						}
						cancel()
						if mode == "exit during withdrawal" {
							time.Sleep(time.Second)
							return failure
						}
						<-exit
						return nil
					},
					shutdown: func(ctx context.Context) error {
						deadline, ok := ctx.Deadline()
						require.True(t, ok)
						require.Equal(t, started.Add(7*time.Second), deadline)
						switch mode {
						case "normal":
							time.Sleep(time.Second)
							close(exit)
						case "drain timeout", "ignored close":
							<-ctx.Done()
							return ctx.Err()
						case "early drain failure":
							return failure
						}
						return nil
					},
					close: func() error {
						forced = true
						if mode != "ignored close" && mode != "early drain failure" {
							close(exit)
						}
						return nil
					},
				}
				a, err := New(svc, Options{ShutdownTimeout: 10 * time.Second, ShutdownDelay: 2 * time.Second, CleanupReserve: 3 * time.Second,
					Hooks: []Hook{{Name: "resource", Stop: func(ctx context.Context) error {
						deadline, ok := ctx.Deadline()
						require.True(t, ok)
						require.Equal(t, started.Add(10*time.Second), deadline)
						require.NoError(t, ctx.Err())
						want := 7 * time.Second
						switch mode {
						case "early exit":
							want = 10 * time.Second
						case "exit during withdrawal":
							want = 9 * time.Second
						case "drain timeout", "ignored close", "early drain failure":
							want = 3 * time.Second
							require.True(t, forced)
						}
						require.Equal(t, want, time.Until(deadline))
						if mode == "ignored close" || mode == "early drain failure" {
							close(exit)
						}
						<-ctx.Done()
						return ctx.Err()
					}}},
				})
				require.NoError(t, err)
				err = a.Run(ctx)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				if mode == "early exit" || mode == "exit during withdrawal" || mode == "early drain failure" {
					require.ErrorIs(t, err, failure)
				}
				require.Equal(t, 10*time.Second, time.Since(started))
			})
		})
	}
}

func TestCleanupReserveDoesNotReduceRollback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("start failed")
		a, err := New(&testService{}, Options{ShutdownTimeout: 10 * time.Second, CleanupReserve: 3 * time.Second,
			Hooks: []Hook{{Name: "resource", Start: func(context.Context) error { time.Sleep(time.Second); return failure }, Stop: func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.Equal(t, 10*time.Second, time.Until(deadline))
				return nil
			}}},
		})
		require.NoError(t, err)
		require.ErrorIs(t, a.Run(context.Background()), failure)
	})
}
