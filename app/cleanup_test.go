package app_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/app"
	"github.com/stretchr/testify/require"
)

func TestCleanupRollsBackPartialCompositionDespitePanic(t *testing.T) {
	var cleanup app.Cleanup
	var events []string
	startErr, closeErr := errors.New("compose failed"), errors.New("close failed")
	group, err := app.NewGroup()
	require.NoError(t, err)
	application, err := app.New(group, app.Options{Hooks: []app.Hook{
		{Name: "database", Stop: func(context.Context) error { events = append(events, "database"); return nil }},
		{Name: "compose", DependsOn: []string{"database"}, Stop: cleanup.Stop, Start: func(context.Context) error {
			if err := cleanup.Add("client", func(ctx context.Context) error {
				require.NoError(t, ctx.Err())
				events = append(events, "client")
				return closeErr
			}); err != nil {
				return err
			}
			if err := cleanup.Add("stream", func(context.Context) error {
				events = append(events, "stream")
				panic(closeErr)
			}); err != nil {
				return err
			}
			return startErr
		}},
	}})
	require.NoError(t, err)
	err = application.Run(context.Background())
	require.ErrorIs(t, err, startErr)
	require.ErrorIs(t, err, closeErr)
	var panicErr *app.PanicError
	require.ErrorAs(t, err, &panicErr)
	require.NotEmpty(t, panicErr.Stack())
	require.Contains(t, err.Error(), "stream")
	require.Contains(t, err.Error(), "client")
	require.Equal(t, []string{"stream", "client", "database"}, events)
	require.ErrorIs(t, cleanup.Stop(context.Background()), closeErr)
	require.Len(t, events, 3)
}

func TestCleanupConcurrentStopUsesFirstContextAndCachesResult(t *testing.T) {
	var cleanup app.Cleanup
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("cleanup failed")
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	require.NoError(t, cleanup.Add("client", func(ctx context.Context) error {
		if ctx != ownerCtx {
			t.Error("cleanup context replaced")
		}
		calls.Add(1)
		close(entered)
		<-release
		return failure
	}))
	done := make(chan error, 1)
	go func() { done <- cleanup.Stop(ownerCtx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	require.Error(t, cleanup.Add("late", func(context.Context) error { return nil }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, cleanup.Stop(ctx), context.Canceled)
	close(release)
	require.ErrorIs(t, <-done, failure)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if !errors.Is(cleanup.Stop(context.Background()), failure) {
				t.Error("cached error lost")
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
}

func TestCleanupValidationAndExpiredContextStillAttemptEveryCallback(t *testing.T) {
	var cleanup app.Cleanup
	var calls int
	closeResource := func(ctx context.Context) error { calls++; return ctx.Err() }
	require.Error(t, cleanup.Stop(nil)) //nolint:staticcheck // Exercise the documented nil-context rejection.
	require.Error(t, cleanup.Add(" ", closeResource))
	require.Error(t, cleanup.Add("nil", nil))
	require.NoError(t, cleanup.Add("one", closeResource))
	require.Error(t, cleanup.Add("one", closeResource))
	require.NoError(t, cleanup.Add("two", closeResource))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, cleanup.Stop(ctx), context.Canceled)
	require.Equal(t, 2, calls)
	require.Error(t, cleanup.Add("three", closeResource))
}

func TestEmptyCleanupStopFreezesRegistration(t *testing.T) {
	var cleanup app.Cleanup
	require.NoError(t, cleanup.Stop(context.Background()))
	require.NoError(t, cleanup.Stop(context.Background()))
	require.Error(t, cleanup.Add("late", func(context.Context) error { return nil }))
}
