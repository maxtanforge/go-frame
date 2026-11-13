package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/maxtanforge/go-frame/concurrency"
	"github.com/stretchr/testify/require"
)

func budget(t *testing.T) *concurrency.Executor {
	t.Helper()
	e, err := concurrency.NewExecutor(concurrency.ExecutorOptions{Concurrency: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestAdmissionIsBoundedAndShutdownDrainsAcceptedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		var delivered []string
		w, err := New(1, budget(t), func(_ context.Context, id string) error {
			delivered = append(delivered, id)
			if id == "first" {
				close(started)
				<-release
			}
			return nil
		}, nil)
		require.NoError(t, err)
		require.NoError(t, w.Submit(context.Background(), "first"))
		run := make(chan error, 1)
		go func() { run <- w.Run() }()
		<-started
		require.NoError(t, w.Submit(context.Background(), "second"))
		require.ErrorIs(t, w.Submit(context.Background(), "overflow"), ErrFull)
		stopped := make(chan error, 1)
		go func() { stopped <- w.Shutdown(context.Background()) }()
		synctest.Wait()
		require.ErrorIs(t, w.Submit(context.Background(), "late"), concurrency.ErrClosed)
		close(release)
		require.NoError(t, <-stopped)
		require.NoError(t, <-run)
		require.Equal(t, []string{"first", "second"}, delivered)
		require.NoError(t, w.Shutdown(context.Background()))
	})
}

func TestShutdownDeadlineCancelsActiveWorkAndLeavesQueuedEventsForReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		calls := 0
		w, err := New(1, budget(t), func(ctx context.Context, _ string) error {
			calls++
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}, nil)
		require.NoError(t, err)
		require.NoError(t, w.Submit(context.Background(), "first"))
		run := make(chan error, 1)
		go func() { run <- w.Run() }()
		<-started
		require.NoError(t, w.Submit(context.Background(), "pending"))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.ErrorIs(t, w.Shutdown(ctx), context.DeadlineExceeded)
		require.NoError(t, <-run)
		require.NoError(t, w.Shutdown(context.Background()))
		require.Equal(t, 1, calls)
	})
}

func TestFailedAttemptIsReportedAndNextEventStillRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("remote unavailable")
		var results []Result
		w, err := New(2, budget(t), func(_ context.Context, id string) error {
			if id == "fail" {
				return failure
			}
			return nil
		}, func(result Result) { results = append(results, result) })
		require.NoError(t, err)
		require.NoError(t, w.Submit(context.Background(), "fail"))
		require.NoError(t, w.Submit(context.Background(), "ok"))
		run := make(chan error, 1)
		go func() { run <- w.Run() }()
		synctest.Wait()
		require.NoError(t, w.Shutdown(context.Background()))
		require.NoError(t, <-run)
		require.Len(t, results, 2)
		require.ErrorIs(t, results[0].Err, failure)
		require.NoError(t, results[1].Err)
	})
}

func TestConcurrentSubmitAndStopAreSafe(t *testing.T) {
	w, err := New(8, budget(t), func(context.Context, string) error { return nil }, nil)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 10 {
				_ = w.Submit(context.Background(), "id")
			}
		})
	}
	wg.Go(func() { _ = w.Close() })
	wg.Wait()
	require.NoError(t, w.Run())
	require.NoError(t, w.Shutdown(context.Background()))
	require.ErrorIs(t, w.Submit(context.Background(), "id"), concurrency.ErrClosed)
}

func TestInvalidOrCanceledSubmissionDoesNotOccupyCapacity(t *testing.T) {
	w, err := New(1, budget(t), func(context.Context, string) error { return nil }, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, w.Submit(ctx, "id"), context.Canceled)
	require.Error(t, w.Submit(context.Background(), ""))
	require.NoError(t, w.Submit(context.Background(), "id"))
	require.NoError(t, w.Close())
	require.NoError(t, w.Shutdown(context.Background()))
	_, err = New(0, budget(t), func(context.Context, string) error { return nil }, nil)
	require.Error(t, err)
	_, err = New(1, nil, func(context.Context, string) error { return nil }, nil)
	require.Error(t, err)
}
