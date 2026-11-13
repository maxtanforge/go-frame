package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type testService struct {
	run      func() error
	shutdown func(context.Context) error
	close    func() error
}

func (s *testService) Run() error                         { return s.run() }
func (s *testService) Shutdown(ctx context.Context) error { return s.shutdown(ctx) }
func (s *testService) Close() error                       { return s.close() }

func TestRunDrainsBeforeStoppingDependencies(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(s string) { mu.Lock(); defer mu.Unlock(); events = append(events, s) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan struct{})
	svc := &testService{
		run:      func() error { record("serve"); cancel(); <-exited; return nil },
		shutdown: func(context.Context) error { record("drain"); close(exited); return nil },
		close:    func() error { t.Error("unexpected force close"); return nil },
	}
	a, err := New(svc, Options{OnShutdown: func() { record("unready") }, Hooks: []Hook{
		{Name: "db", Start: func(context.Context) error { record("db+"); return nil }, Stop: func(context.Context) error { record("db-"); return nil }},
		{Name: "cache", Start: func(context.Context) error { record("cache+"); return nil }, Stop: func(context.Context) error { record("cache-"); return nil }},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"db+", "cache+", "serve", "unready", "drain", "cache-", "db-"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v, want=%v", events, want)
	}
	if err := a.Run(ctx); err == nil {
		t.Fatal("second Run must fail")
	}
}

func TestStartupFailureRollsBackIncludingFailedHook(t *testing.T) {
	failure := errors.New("connect failed")
	cleanup := errors.New("close failed")
	var events []string
	svc := &testService{run: func() error { t.Fatal("must not serve"); return nil }}
	a, err := New(svc, Options{Hooks: []Hook{
		{Name: "a", Start: func(context.Context) error { events = append(events, "a+"); return nil }, Stop: func(context.Context) error { events = append(events, "a-"); return cleanup }},
		{Name: "b", Start: func(context.Context) error { events = append(events, "b+"); return failure }, Stop: func(context.Context) error { events = append(events, "b-"); return nil }},
		{Name: "c", Start: func(context.Context) error { t.Fatal("must not start"); return nil }},
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = a.Run(context.Background())
	if !errors.Is(err, failure) || !errors.Is(err, cleanup) {
		t.Fatalf("lost errors: %v", err)
	}
	if !reflect.DeepEqual(events, []string{"a+", "b+", "b-", "a-"}) {
		t.Fatalf("events=%v", events)
	}
}

func TestServiceFailureStillCleansUp(t *testing.T) {
	failure := errors.New("bind failed")
	stopped := false
	svc := &testService{run: func() error { return failure }, shutdown: func(context.Context) error { return nil }, close: func() error { return nil }}
	a, err := New(svc, Options{Hooks: []Hook{{Name: "resource", Stop: func(context.Context) error { stopped = true; return nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("err=%v", err)
	}
	if !stopped {
		t.Fatal("resource not closed")
	}
}

func TestShutdownTimeoutForcesCloseBeforeDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan struct{})
	var events []string
	svc := &testService{
		run:      func() error { cancel(); <-exited; return nil },
		shutdown: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		close:    func() error { events = append(events, "close"); close(exited); return nil },
	}
	a, err := New(svc, Options{ShutdownTimeout: 10 * time.Millisecond, Hooks: []Hook{{Name: "db", Stop: func(context.Context) error { events = append(events, "stop"); return nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(events, []string{"close", "stop"}) {
		t.Fatalf("events=%v", events)
	}
}

func TestStartupDeadlineAndValidation(t *testing.T) {
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("nil service accepted")
	}
	var nilService *testService
	if _, err := New(nilService, Options{}); err == nil {
		t.Fatal("typed nil service accepted")
	}
	svc := &testService{run: func() error { t.Fatal("must not serve"); return nil }}
	if _, err := New(svc, Options{ShutdownTimeout: -time.Second}); err == nil {
		t.Fatal("negative timeout accepted")
	}
	if _, err := New(svc, Options{Hooks: []Hook{{Name: "x"}, {Name: "x"}}}); err == nil {
		t.Fatal("invalid hooks accepted")
	}
	stopped := false
	a, err := New(svc, Options{StartupTimeout: time.Millisecond, Hooks: []Hook{{Name: "slow", Start: func(ctx context.Context) error { <-ctx.Done(); return nil }, Stop: func(context.Context) error { stopped = true; return nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Run(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if !stopped {
		t.Fatal("startup timeout leaked resource")
	}
}

func TestShutdownRetainsAlreadyReturnedServiceError(t *testing.T) {
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		failure := errors.New("service exit failure")
		svc := &testService{
			run:      func() error { cancel(); <-entered; return failure },
			shutdown: func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() },
			close:    func() error { return nil },
		}
		a, err := New(svc, Options{ShutdownTimeout: 5 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		err = a.Run(ctx)
		cancel()
		if !errors.Is(err, failure) {
			t.Fatalf("lost available service error: %v", err)
		}
	}
}
