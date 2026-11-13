package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestHookPanicsRetainDiagnosticsAndContinueCleanup(t *testing.T) {
	for _, startupPanic := range []bool{true, false} {
		t.Run(fmt.Sprintf("startup=%t", startupPanic), func(t *testing.T) {
			startFailure := errors.New("start failed")
			stopFailure := errors.New("stop failed")
			var stopped []string
			svc := &testService{run: func() error { t.Fatal("must not serve"); return nil }}
			a, err := New(svc, Options{Hooks: []Hook{
				{Name: "first", Stop: func(context.Context) error { stopped = append(stopped, "first"); return stopFailure }},
				{Name: "second", Start: func(context.Context) error {
					if startupPanic {
						panic(startFailure)
					}
					return startFailure
				}, Stop: func(context.Context) error { stopped = append(stopped, "second"); panic("cleanup panic") }},
			}})
			if err != nil {
				t.Fatal(err)
			}
			// Recover at the test boundary to report the missing library boundary.
			var escaped any
			func() { defer func() { escaped = recover() }(); err = a.Run(context.Background()) }()
			if escaped != nil {
				t.Fatalf("hook panic escaped Run: %v; stopped=%v", escaped, stopped)
			}
			if !errors.Is(err, startFailure) || !errors.Is(err, stopFailure) {
				t.Fatalf("lost joined errors: %v", err)
			}
			var diagnostic *PanicError
			if !errors.As(err, &diagnostic) || len(diagnostic.Stack()) == 0 {
				t.Fatalf("missing panic diagnostic: %v", err)
			}
			if !reflect.DeepEqual(stopped, []string{"second", "first"}) {
				t.Fatalf("cleanup order: %v", stopped)
			}
		})
	}
}

func panicSite(value any) error { panic(value) }

func TestShutdownNotificationPanicContinuesCleanup(t *testing.T) {
	for _, value := range []any{errors.New("notification failed"), "notification failed"} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			cleanup := errors.New("cleanup failed")
			var events []string
			svc := &testService{
				run:      func() error { return nil },
				shutdown: func(context.Context) error { events = append(events, "drain"); return nil },
				close:    func() error { t.Error("unexpected force close"); return nil },
			}
			a, err := New(svc, Options{OnShutdown: func() { _ = panicSite(value) }, Hooks: []Hook{
				{Name: "first", Stop: func(context.Context) error { events = append(events, "first"); return cleanup }},
				{Name: "second", Stop: func(context.Context) error { events = append(events, "second"); return nil }},
			}})
			if err != nil {
				t.Fatal(err)
			}
			var escaped any
			func() { defer func() { escaped = recover() }(); err = a.Run(context.Background()) }()
			if escaped != nil {
				t.Fatalf("notification panic escaped: %v; cleanup=%v", escaped, events)
			}
			var diagnostic *PanicError
			if !errors.As(err, &diagnostic) || diagnostic.Value() != value || !bytes.Contains(diagnostic.Stack(), []byte("app.panicSite")) {
				t.Fatalf("lost notification diagnostic: %v", err)
			}
			if cause, ok := value.(error); ok && !errors.Is(err, cause) {
				t.Fatal("lost panic cause")
			}
			if !errors.Is(err, cleanup) {
				t.Fatal("lost cleanup error")
			}
			if !reflect.DeepEqual(events, []string{"drain", "second", "first"}) {
				t.Fatalf("cleanup order: %v", events)
			}
		})
	}
}

func TestDirectServiceRunPanicDrainsAndCleansUp(t *testing.T) {
	cause := errors.New("service failed")
	cleanup := errors.New("cleanup failed")
	for _, value := range []any{cause, "non-error panic"} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			var events []string
			svc := &testService{
				run:      func() error { return panicSite(value) },
				shutdown: func(context.Context) error { events = append(events, "drain"); return nil },
				close:    func() error { t.Error("unexpected force close"); return nil },
			}
			a, err := New(svc, Options{OnShutdown: func() { events = append(events, "unready") }, Hooks: []Hook{
				{Name: "first", Stop: func(context.Context) error { events = append(events, "first"); return nil }},
				{Name: "second", Stop: func(context.Context) error { events = append(events, "second"); return cleanup }},
			}})
			if err != nil {
				t.Fatal(err)
			}
			err = a.Run(context.Background())
			var diagnostic *PanicError
			if !errors.As(err, &diagnostic) || diagnostic.Value() != value {
				t.Fatalf("missing panic diagnostic: %v", err)
			}
			if !bytes.Contains(diagnostic.Stack(), []byte("app.panicSite")) {
				t.Fatal("panic site missing")
			}
			if original, ok := value.(error); ok && !errors.Is(err, original) {
				t.Fatal("original cause lost")
			}
			if !errors.Is(err, cleanup) {
				t.Fatal("cleanup failure lost")
			}
			if !reflect.DeepEqual(events, []string{"unready", "drain", "second", "first"}) {
				t.Fatalf("shutdown order: %v", events)
			}
		})
	}
}

func TestPanicDiagnosticsSurviveServiceWrapping(t *testing.T) {
	cause := errors.New("worker failed")
	for _, value := range []any{cause, "non-error panic"} {
		task, err := NewTask(func(context.Context) error { return panicSite(value) })
		if err != nil {
			t.Fatal(err)
		}
		group, err := NewGroup(NamedService{Name: "worker", Service: task})
		if err != nil {
			t.Fatal(err)
		}
		runErr := group.Run()
		if runErr == nil || !strings.Contains(runErr.Error(), "worker") {
			t.Fatalf("run error: %v", runErr)
		}
		var diagnostic *PanicError
		if !errors.As(runErr, &diagnostic) {
			t.Fatalf("missing panic diagnostic: %v", runErr)
		}
		if diagnostic.Value() != value {
			t.Fatalf("panic value: %v", diagnostic.Value())
		}
		if !bytes.Contains(diagnostic.Stack(), []byte("app.panicSite")) {
			t.Fatalf("panic site missing: %s", diagnostic.Stack())
		}
		stack := diagnostic.Stack()
		stack[0] ^= 0xff
		if bytes.Equal(stack, diagnostic.Stack()) {
			t.Fatal("caller can mutate retained stack")
		}
		if strings.Contains(diagnostic.Error(), "goroutine ") {
			t.Fatal("Error implicitly dumps stack")
		}
		if value == cause && !errors.Is(runErr, cause) { //nolint:errorlint // panic 载荷身份断言
			t.Fatal("original error lost")
		}
		if value != cause && diagnostic.Unwrap() != nil { //nolint:errorlint // 同上
			t.Fatal("non-error panic acquired a cause")
		}
		stopErr := group.Shutdown(bounded(t))
		if !errors.Is(stopErr, diagnostic) {
			t.Fatalf("shutdown lost diagnostic: %v", stopErr)
		}
	}
}
