package devicesync

import (
	"context"
	"sort"
	"testing"
	"testing/synctest"

	"github.com/maxtanforge/go-frame/concurrency"
	"github.com/maxtanforge/go-frame/examples/business/internal/devicesync/worker"
	"github.com/stretchr/testify/require"
)

func TestDurableAcceptanceSurvivesFullQueueAndProcessStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		store := &memoryStore{records: map[string]Record{}}
		budget, err := concurrency.NewExecutor(concurrency.ExecutorOptions{Concurrency: 1})
		require.NoError(t, err)
		t.Cleanup(func() { _ = budget.Close() })
		started := make(chan struct{})
		first := Service{Store: store, Sink: sinkFunc(func(ctx context.Context, _ Event) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})}
		w, err := worker.New(1, budget, first.Deliver, nil)
		require.NoError(t, err)
		for _, id := range []string{"one", "two", "three"} {
			accepted, err := first.Accept(ctx, Event{ID: id, UserID: "user-1", Steps: 10})
			require.NoError(t, err)
			require.False(t, accepted.Delivered)
		}
		require.NoError(t, w.Submit(ctx, "one"))
		run := make(chan error, 1)
		go func() { run <- w.Run() }()
		<-started
		require.NoError(t, w.Submit(ctx, "two"))
		require.ErrorIs(t, w.Submit(ctx, "three"), worker.ErrFull)
		require.NoError(t, w.Close())
		require.NoError(t, <-run)
		require.NoError(t, w.Shutdown(ctx))
		// A restarted composition reads durable pending IDs (Store.Pending in
		// PostgreSQL); it does not reconstruct work from the old in-memory queue.
		pending := make([]string, 0, 3)
		for id, r := range store.records {
			if !r.Delivered {
				pending = append(pending, id)
			}
		}
		sort.Strings(pending)
		require.Len(t, pending, 3)
		remote := map[string]Event{}
		restarted := Service{Store: store, Sink: sinkFunc(func(_ context.Context, e Event) error { remote[e.ID] = e; return nil })}
		w2, err := worker.New(3, budget, restarted.Deliver, nil)
		require.NoError(t, err)
		for _, id := range pending {
			require.NoError(t, w2.Submit(ctx, id))
		}
		run2 := make(chan error, 1)
		go func() { run2 <- w2.Run() }()
		synctest.Wait()
		require.NoError(t, w2.Shutdown(ctx))
		require.NoError(t, <-run2)
		require.Len(t, remote, 3)
		for _, r := range store.records {
			require.True(t, r.Delivered)
		}
	})
}
