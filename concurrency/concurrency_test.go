package concurrency

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMapOrderedAndBounded(t *testing.T) {
	const workers = 3
	inputs := make([]int, 100)
	for i := range inputs {
		inputs[i] = i
	}
	started := make(chan struct{}, len(inputs))
	gate := make(chan struct{})
	var active, peak atomic.Int32
	done := make(chan struct{})
	var got []int
	var err error
	go func() {
		defer close(done)
		got, err = Map(context.Background(), inputs, workers, func(_ context.Context, n int) (int, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); current > old; old = peak.Load() {
				if peak.CompareAndSwap(old, current) {
					break
				}
			}
			started <- struct{}{}
			<-gate
			return n * 2, nil
		})
	}()
	for range workers {
		waitSignal(t, started)
	}
	close(gate)
	waitSignal(t, done)
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() != workers || active.Load() != 0 {
		t.Fatalf("peak=%d active=%d", peak.Load(), active.Load())
	}
	for i := range inputs {
		inputs[i] *= 2
	}
	if !reflect.DeepEqual(got, inputs) {
		t.Fatalf("unordered results: %v", got)
	}
}

func TestMapFailureCancelsAndWaitsForCallbacks(t *testing.T) {
	want := errors.New("failed")
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var got []int
	var err error
	var calls atomic.Int32
	go func() {
		defer close(done)
		got, err = Map(context.Background(), []int{0, 1, 2, 3}, 2, func(ctx context.Context, n int) (int, error) {
			calls.Add(1)
			if n == 0 {
				<-started
				return 0, want
			}
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
			return n, ctx.Err()
		})
	}()
	waitSignal(t, canceled)
	select {
	case <-done:
		t.Error("returned before callback completed")
	default:
	}
	close(release)
	waitSignal(t, done)
	if !errors.Is(err, want) || got != nil || calls.Load() != 2 {
		t.Fatalf("results=%v error=%v calls=%d", got, err, calls.Load())
	}
}

func TestMapValidationAndCancellation(t *testing.T) {
	fn := func(_ context.Context, n int) (int, error) { return n, nil }
	for _, workers := range []int{-1, 0} {
		if _, err := Map(context.Background(), []int{}, workers, fn); err == nil {
			t.Fatal("accepted invalid workers")
		}
	}
	if _, err := Map(nil, []int{}, 1, fn); err == nil { //nolint:staticcheck // 负向测试:显式验证 nil context 被拒绝
		t.Fatal("accepted nil context")
	}
	if _, err := Map[int, int](context.Background(), nil, 1, nil); err == nil {
		t.Fatal("accepted nil callback")
	}
	got, err := Map(context.Background(), []int{}, 1, fn)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = Map(ctx, []int{1}, 1, func(context.Context, int) (int, error) { t.Error("called with canceled context"); return 0, nil })
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancel: %v %v", got, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	got, err = Map(ctx, []int{1}, 1, func(_ context.Context, n int) (int, error) { cancel(); return n, nil })
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("callback cancellation: %v %v", got, err)
	}
}

func TestLimiterCapacityAndIdempotentRelease(t *testing.T) {
	limiter, err := NewLimiter(2)
	if err != nil {
		t.Fatal(err)
	}
	a, err := limiter.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, ok := limiter.TryAcquire()
	if !ok || limiter.InUse() != 2 {
		t.Fatal("did not admit to capacity")
	}
	if release, ok := limiter.TryAcquire(); ok || release != nil {
		t.Fatal("admitted above capacity")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(a)
	}
	wg.Wait()
	if limiter.InUse() != 1 {
		t.Fatal("duplicate release freed extra slots")
	}
	b()
	if limiter.InUse() != 0 {
		t.Fatal("release leaked slot")
	}
}

func TestLimiterCancellationAndWaiting(t *testing.T) {
	limiter, _ := NewLimiter(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 100 {
		if release, err := limiter.Acquire(ctx); !errors.Is(err, context.Canceled) || release != nil {
			t.Fatalf("pre-canceled acquire: %v", err)
		}
	}
	if limiter.InUse() != 0 {
		t.Fatal("cancellation leaked slots")
	}
	release, _ := limiter.Acquire(context.Background())
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if r, err := limiter.Acquire(ctx); !errors.Is(err, context.Canceled) || r != nil {
			t.Errorf("waiting cancel: %v", err)
		}
	}()
	cancel()
	waitSignal(t, done)
	release()
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		release, _ := limiter.Acquire(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			r, err := limiter.Acquire(ctx)
			if err == nil {
				r()
			} else if !errors.Is(err, context.Canceled) {
				t.Errorf("race: %v", err)
			}
		}()
		cancel()
		release()
		waitSignal(t, done)
		if limiter.InUse() != 0 {
			t.Fatal("racing cancellation leaked slot")
		}
	}
	if r, err := limiter.Acquire(nil); err == nil || r != nil { //nolint:staticcheck // 负向测试:显式验证 nil context 被拒绝
		t.Fatal("accepted nil context")
	}
	for _, capacity := range []int{-1, 0} {
		if _, err := NewLimiter(capacity); err == nil {
			t.Fatal("accepted invalid capacity")
		}
	}
}

func TestLimiterBoundsConcurrentCallers(t *testing.T) {
	const capacity = 3
	limiter, _ := NewLimiter(capacity)
	gate := make(chan struct{})
	started := make(chan struct{}, 30)
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() {
			release, err := limiter.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			current := active.Add(1)
			for old := peak.Load(); current > old; old = peak.Load() {
				if peak.CompareAndSwap(old, current) {
					break
				}
			}
			started <- struct{}{}
			<-gate
			active.Add(-1)
			release()
		})
	}
	for range capacity {
		waitSignal(t, started)
	}
	if limiter.InUse() != capacity {
		t.Errorf("in use=%d", limiter.InUse())
	}
	close(gate)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	waitSignal(t, done)
	if peak.Load() != capacity || limiter.InUse() != 0 {
		t.Fatalf("peak=%d in use=%d", peak.Load(), limiter.InUse())
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}
