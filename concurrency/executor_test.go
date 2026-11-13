package concurrency_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/concurrency"
)

func TestExecutorBudgetAndGracefulDrain(t *testing.T) {
	e, err := concurrency.NewExecutor(concurrency.ExecutorOptions{Concurrency: 1, MaxWaiters: 1, WaitTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	active := make(chan error, 1)
	go func() {
		active <- e.Do(ctx, func(ctx context.Context) error {
			close(entered)
			select {
			case <-release:
				return ctx.Err()
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waiter := make(chan error, 1)
	go func() {
		waiter <- e.Do(ctx, func(context.Context) error { t.Error("waiting callback ran during shutdown"); return nil })
	}()
	for e.Stats().Waiting != 1 {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
	if err := e.Do(ctx, func(context.Context) error { t.Fatal("overload executed"); return nil }); !errors.Is(err, concurrency.ErrOverloaded) {
		t.Fatal(err)
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- e.Shutdown(ctx) }()
	select {
	case err := <-waiter:
		if !errors.Is(err, concurrency.ErrClosed) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-shutdown:
		t.Fatalf("did not drain: %v", err)
	default:
	}
	close(release)
	if err := <-active; err != nil {
		t.Fatal(err)
	}
	if err := <-shutdown; err != nil {
		t.Fatal(err)
	}
	if err := e.Do(ctx, func(context.Context) error { return nil }); !errors.Is(err, concurrency.ErrClosed) {
		t.Fatal(err)
	}
	if stats := e.Stats(); stats.InUse != 0 || stats.Waiting != 0 || stats.Rejected != 1 {
		t.Fatalf("%+v", stats)
	}
}

func TestExecutorTimeoutAndPanicRelease(t *testing.T) {
	e, err := concurrency.NewExecutor(concurrency.ExecutorOptions{Concurrency: 1, RunTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	if err := e.Do(context.Background(), func(ctx context.Context) error { <-ctx.Done(); return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		_ = e.Do(context.Background(), func(context.Context) error { panic("business panic") })
	}()
	if err := e.Do(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Do(ctx, func(context.Context) error { t.Fatal("canceled executed"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExecutorShutdownDeadlineCancelsActiveWork(t *testing.T) {
	e, err := concurrency.NewExecutor(concurrency.ExecutorOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	entered, finished := make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- e.Do(context.Background(), func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() })
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("active work not canceled")
	}
	if err := e.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
