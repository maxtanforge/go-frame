package health

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// Models the observed scheduling window where Deadline has elapsed but the
// timer's cancellation has not been published through Err/Done yet.
type pendingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c pendingDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestProbeRejectsElapsedDeadlineBeforeCancellationPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := pendingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(time.Second)}
		called := false
		err := probe(ctx, func(context.Context) error {
			called = true
			time.Sleep(2 * time.Second) // Virtual time; no wall-clock delay.
			return nil
		})
		if !called || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("called=%v error=%v; want late-success rejection", called, err)
		}
		called = false
		err = probe(ctx, func(context.Context) error { called = true; return nil })
		if called || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("called=%v error=%v; want no probe after deadline", called, err)
		}
	})
}

func TestProbeDoesNotStartAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := probe(ctx, func(context.Context) error { called = true; return nil })
	if called || !errors.Is(err, context.Canceled) {
		t.Fatalf("called=%v error=%v; want no invocation and context cancellation", called, err)
	}
}

func TestProbeRejectsSuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := probe(ctx, func(context.Context) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v; want context cancellation", err)
	}
}

func TestCollectResultsRetainsBufferedRequiredAtDeadline(t *testing.T) {
	// All required probes finished before collection resumes at the deadline.
	// Only the optional probe has no result. Repetitions expose the old random
	// select branch without scheduler coordination or sleep-based timing.
	for range 16 {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		results := make(chan result, 9)
		for i := range 8 {
			results <- result{index: i}
		}
		states := collectResults(ctx, results, 9)
		cancel()
		for i := range 8 {
			if states[i] != nil {
				t.Fatalf("completed required check %d: %v; want healthy", i, states[i])
			}
		}
		if !errors.Is(states[8], context.DeadlineExceeded) {
			t.Fatalf("missing optional check: %v; want deadline exceeded", states[8])
		}
	}
}

func TestProbePreservesFailureAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cause := errors.New("dependency failed")
	err := probe(ctx, func(context.Context) error { cancel(); return cause })
	if !errors.Is(err, cause) {
		t.Fatalf("error=%v; want dependency failure", err)
	}
}

func TestProbeRejectsSuccessAfterActualDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := probe(ctx, func(ctx context.Context) error { <-ctx.Done(); return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v; want deadline exceeded", err)
	}
}
