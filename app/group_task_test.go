package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}
func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-bounded(t).Done():
		t.Fatal("timed out")
	}
}

func TestGroupAppDrainsSiblingsBeforeResources(t *testing.T) {
	failure, siblingFailure, drainFailure := errors.New("first"), errors.New("sibling"), errors.New("drain")
	started, stop, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	sibling := &testService{run: func() error { close(started); <-stop; defer close(exited); return siblingFailure }, shutdown: func(context.Context) error { close(stop); return drainFailure }, close: func() error { return nil }}
	first := &testService{run: func() error { <-started; return failure }, shutdown: func(context.Context) error { return nil }, close: func() error { return nil }}
	g, err := NewGroup(NamedService{Name: "first", Service: first}, NamedService{Name: "sibling", Service: sibling})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(g, Options{ShutdownTimeout: time.Second, Hooks: []Hook{{Name: "resource", Stop: func(context.Context) error {
		select {
		case <-exited:
		default:
			t.Error("resource stopped before sibling")
		}
		return nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	err = a.Run(bounded(t))
	for _, want := range []error{failure, siblingFailure, drainFailure} {
		if !errors.Is(err, want) {
			t.Errorf("lost %v in %v", want, err)
		}
	}
	if !strings.Contains(err.Error(), "sibling") {
		t.Fatal(err)
	}
	if g.Run() == nil {
		t.Fatal("second Run accepted")
	}
}
func TestGroupEarlyExitAndPanic(t *testing.T) {
	for _, panicRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil", true: "panic"}[panicRun], func(t *testing.T) {
			s := &testService{run: func() error {
				if panicRun {
					panic("boom")
				}
				return nil
			}, shutdown: func(context.Context) error { return nil }, close: func() error { return nil }}
			g, _ := NewGroup(NamedService{Name: "critical", Service: s})
			done := make(chan error, 1)
			go func() { done <- g.Run() }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "critical") {
					t.Fatalf("err=%v", err)
				}
			case <-bounded(t).Done():
				t.Fatal("Run hung")
			}
			_ = g.Shutdown(bounded(t))
		})
	}
}
func TestGroupStopBeforeRunAndConcurrentClose(t *testing.T) {
	var runs, closes, stops atomic.Int32
	fail := errors.New("close")
	s := &testService{run: func() error { runs.Add(1); return nil }, shutdown: func(context.Context) error { stops.Add(1); return nil }, close: func() error { closes.Add(1); return fail }}
	g, _ := NewGroup(NamedService{Name: "s", Service: s})
	if err := g.Shutdown(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if err := g.Run(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			if !errors.Is(g.Close(), fail) {
				t.Error("close error lost")
			}
			if err := g.Shutdown(bounded(t)); !errors.Is(err, fail) {
				t.Errorf("shutdown lost cached close error: %v", err)
			}
		})
	}
	wg.Wait()
	if runs.Load() != 0 || closes.Load() != 1 || stops.Load() != 1 {
		t.Fatalf("runs/closes/stops %d/%d/%d", runs.Load(), closes.Load(), stops.Load())
	}
}
func TestGroupShutdownBudgetAndSharedContext(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	s := &testService{run: func() error { close(entered); <-release; return nil }, shutdown: func(got context.Context) error {
		if got != ctx {
			t.Error("different budget")
		}
		<-got.Done()
		return got.Err()
	}, close: func() error { close(release); return nil }}
	g, _ := NewGroup(NamedService{Name: "slow", Service: s})
	done := make(chan struct{})
	go func() { _ = g.Run(); close(done) }()
	waitSignal(t, entered)
	if !errors.Is(g.Shutdown(ctx), context.DeadlineExceeded) {
		t.Fatal("missing deadline")
	}
	_ = g.Close()
	waitSignal(t, done)
}
func TestGroupValidation(t *testing.T) {
	var typed *testService
	for _, members := range [][]NamedService{{{Name: "", Service: &testService{}}}, {{Name: "x", Service: typed}}, {{Name: "x", Service: &testService{}}, {Name: "x", Service: &testService{}}}} {
		if _, err := NewGroup(members...); err == nil {
			t.Fatal("invalid group accepted")
		}
	}
}

func TestTaskCancellationWaitsAndIsOneShot(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	task, err := NewTask(func(ctx context.Context) error { close(entered); <-ctx.Done(); defer close(exited); return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- task.Run() }()
	waitSignal(t, entered)
	if err := task.Shutdown(bounded(t)); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, exited)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if task.Run() == nil {
		t.Fatal("second Run accepted")
	}
}
func TestTaskPreStopAndConcurrentStop(t *testing.T) {
	for _, force := range []bool{false, true} {
		var calls atomic.Int32
		task, _ := NewTask(func(context.Context) error { calls.Add(1); return nil })
		if force {
			_ = task.Close()
		} else {
			_ = task.Shutdown(bounded(t))
		}
		if err := task.Run(); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Go(func() { _ = task.Close(); _ = task.Shutdown(bounded(t)) })
		}
		wg.Wait()
		if calls.Load() != 0 {
			t.Fatal("started after stop")
		}
	}
}
func TestTaskShutdownDeadlineAndFailure(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("task failure")
	task, _ := NewTask(func(context.Context) error { close(entered); <-release; return failure })
	done := make(chan struct{})
	go func() { _ = task.Run(); close(done) }()
	waitSignal(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(task.Shutdown(ctx), context.Canceled) {
		t.Fatal("missing cancellation")
	}
	_ = task.Close()
	close(release)
	waitSignal(t, done)
	if !errors.Is(task.Shutdown(bounded(t)), failure) {
		t.Fatal("failure lost")
	}
	if _, err := NewTask(nil); err == nil {
		t.Fatal("nil accepted")
	}
}

func TestGroupShutdownStartsEveryMemberBeforeWaiting(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	makeService := func() Service {
		return &testService{
			run:      func() error { <-release; return nil },
			shutdown: func(context.Context) error { entered <- struct{}{}; <-release; return nil },
			close:    func() error { return nil },
		}
	}
	g, _ := NewGroup(NamedService{Name: "a", Service: makeService()}, NamedService{Name: "b", Service: makeService()})
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(bounded(t)) }()
	waitSignal(t, entered)
	waitSignal(t, entered)
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-bounded(t).Done():
		t.Fatal("shutdown hung")
	}
}

func TestTaskPanicIsError(t *testing.T) {
	failure := errors.New("callback panic")
	task, _ := NewTask(func(context.Context) error { panic(failure) })
	if err := task.Run(); !errors.Is(err, failure) {
		t.Fatalf("panic cause lost: %v", err)
	}
	if err := task.Shutdown(bounded(t)); !errors.Is(err, failure) {
		t.Fatalf("shutdown lost cause: %v", err)
	}
}

func TestGroupCloseBeforeRun(t *testing.T) {
	var calls atomic.Int32
	g, _ := NewGroup(NamedService{Name: "stopped", Service: &testService{run: func() error { calls.Add(1); return nil }, shutdown: func(context.Context) error { return nil }, close: func() error { return nil }}})
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Run(); err != nil {
		t.Fatal(err)
	}
	if err := g.Shutdown(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("started after close")
	}
}

func TestGroupAppClosesFailedDrainBeforeBudgetExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	failedRelease, failedExited, gracefulExited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	gracefulRelease := make(chan struct{})
	drainErr, closeErr := errors.New("drain failed"), errors.New("close failed")
	var closes atomic.Int32
	failed := &testService{
		run:      func() error { started <- struct{}{}; <-failedRelease; close(failedExited); return nil },
		shutdown: func(context.Context) error { return drainErr },
		close: func() error {
			if closes.Add(1) == 1 {
				close(failedRelease)
			}
			return closeErr
		},
	}
	graceful := &testService{
		run: func() error { started <- struct{}{}; <-gracefulRelease; close(gracefulExited); return nil },
		shutdown: func(ctx context.Context) error {
			select {
			case <-failedExited:
				close(gracefulRelease)
				return nil
			case <-ctx.Done():
				close(gracefulRelease)
				return ctx.Err()
			}
		},
		close: func() error { return nil },
	}
	g, _ := NewGroup(NamedService{Name: "failed", Service: failed}, NamedService{Name: "graceful", Service: graceful})
	a, _ := New(g, Options{ShutdownTimeout: 100 * time.Millisecond, Hooks: []Hook{{Name: "resource", Stop: func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Error("resource cleanup received expired budget")
		}
		for _, exited := range []<-chan struct{}{failedExited, gracefulExited} {
			select {
			case <-exited:
			default:
				t.Error("resource closed while service still running")
			}
		}
		return nil
	}}}})
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	waitSignal(t, started)
	waitSignal(t, started)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, drainErr) || !errors.Is(err, closeErr) {
			t.Fatalf("lost drain/close errors: %v", err)
		}
	case <-bounded(t).Done():
		t.Fatal("App hung")
	}
	if closes.Load() != 1 {
		t.Fatalf("failed child closed %d times", closes.Load())
	}
	if err := g.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("cached close error lost: %v", err)
	}
	if closes.Load() != 1 {
		t.Fatal("repeated Close reached child")
	}
}

func TestTaskWrappedCancellationAfterStopIsCompletion(t *testing.T) {
	// 回调把 context.Canceled 包装进业务错误是常见形态；停机后的此类错误
	// 必须按正常完成处理，而不是向调用方上报为任务失败。
	entered := make(chan struct{})
	task, err := NewTask(func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return fmt.Errorf("shutdown cleanup: %w", context.Canceled)
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- task.Run() }()
	waitSignal(t, entered)
	if err := task.Shutdown(bounded(t)); err != nil {
		t.Fatalf("shutdown reported wrapped cancellation as failure: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("wrapped cancellation after stop reported as error: %v", err)
	}
}
