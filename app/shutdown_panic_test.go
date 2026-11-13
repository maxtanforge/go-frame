package app

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceStopPanicContinuesResourceCleanup(t *testing.T) {
	for _, mode := range []string{"shutdown", "close", "deadline-close"} {
		for _, payload := range []any{errors.New("stop panic"), "stop panic"} {
			t.Run(mode+"/"+reflect.TypeOf(payload).String(), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				exit := make(chan struct{})
				var events []string
				runFailure := errors.New("run failed")
				drainFailure := errors.New("drain failed")
				cleanupFailure := errors.New("cleanup failed")
				svc := &testService{
					run: func() error {
						if mode == "deadline-close" {
							cancel()
							<-exit
						}
						return runFailure
					},
					shutdown: func(context.Context) error {
						events = append(events, "drain")
						if mode == "shutdown" {
							return panicSite(payload)
						}
						if mode == "close" {
							return drainFailure
						}
						return nil
					},
					close: func() error {
						events = append(events, "close")
						close(exit)
						if mode != "shutdown" {
							return panicSite(payload)
						}
						return nil
					},
				}
				a, err := New(svc, Options{ShutdownTimeout: 100 * time.Millisecond, Hooks: []Hook{
					{Name: "first", Stop: func(context.Context) error { events = append(events, "first"); return nil }},
					{Name: "second", Stop: func(context.Context) error { events = append(events, "second"); return cleanupFailure }},
				}})
				if err != nil {
					t.Fatal(err)
				}
				var escaped any
				func() { defer func() { escaped = recover() }(); err = a.Run(ctx) }()
				if escaped != nil {
					t.Fatalf("stop panic escaped: %v; events=%v", escaped, events)
				}
				assertStopPanic(t, err, payload)
				if !errors.Is(err, cleanupFailure) {
					t.Fatalf("cleanup error lost: %v", err)
				}
				if mode != "deadline-close" && !errors.Is(err, runFailure) {
					t.Fatalf("run error lost: %v", err)
				}
				if mode == "close" && !errors.Is(err, drainFailure) {
					t.Fatalf("drain error lost: %v", err)
				}
				if mode == "deadline-close" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("deadline error lost: %v", err)
				}
				if !reflect.DeepEqual(events, []string{"drain", "close", "second", "first"}) {
					t.Fatalf("stop order: %v", events)
				}
			})
		}
	}
}

func TestGroupStopPanicPreservesSiblingCleanupAndCloseCache(t *testing.T) {
	for _, mode := range []string{"shutdown", "close-after-shutdown", "close"} {
		for _, payload := range []any{errors.New("child stop panic"), "child stop panic"} {
			t.Run(mode+"/"+reflect.TypeOf(payload).String(), func(t *testing.T) {
				var brokenCloses, siblingCloses, siblingDrains atomic.Int32
				drainFailure := errors.New("drain failed")
				closeFailure := errors.New("close failed")
				g, err := NewGroup(
					NamedService{Name: "broken", Service: &testService{
						shutdown: func(context.Context) error {
							if mode == "shutdown" {
								return panicSite(payload)
							}
							return drainFailure
						},
						close: func() error {
							brokenCloses.Add(1)
							if mode != "shutdown" {
								return panicSite(payload)
							}
							return closeFailure
						},
					}},
					NamedService{Name: "sibling", Service: &testService{
						shutdown: func(context.Context) error { siblingDrains.Add(1); return nil },
						close:    func() error { siblingCloses.Add(1); return nil },
					}},
				)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "close" {
					err = g.Close()
				} else {
					err = g.Shutdown(bounded(t))
					if siblingDrains.Load() != 1 {
						t.Fatal("sibling did not drain")
					}
					if mode == "shutdown" && !errors.Is(err, closeFailure) || mode == "close-after-shutdown" && !errors.Is(err, drainFailure) {
						t.Fatalf("stop cause lost: %v", err)
					}
				}
				assertStopPanic(t, err, payload)
				if !strings.Contains(err.Error(), "broken") {
					t.Fatalf("member name lost: %v", err)
				}
				for range 2 {
					closeErr := g.Close()
					if mode == "shutdown" {
						if !errors.Is(closeErr, closeFailure) {
							t.Fatalf("cached close cause lost: %v", closeErr)
						}
					} else {
						assertStopPanic(t, closeErr, payload)
					}
				}
				if brokenCloses.Load() != 1 || siblingCloses.Load() != 1 {
					t.Fatalf("close counts: broken=%d sibling=%d", brokenCloses.Load(), siblingCloses.Load())
				}
			})
		}
	}
}

func assertStopPanic(t *testing.T, err error, payload any) {
	t.Helper()
	var diagnostic *PanicError
	if !errors.As(err, &diagnostic) || diagnostic.Value() != payload {
		t.Fatalf("panic diagnostic lost: %v", err)
	}
	if !bytes.Contains(diagnostic.Stack(), []byte("app.panicSite")) {
		t.Fatal("panic site lost")
	}
	if cause, ok := payload.(error); ok && !errors.Is(err, cause) {
		t.Fatalf("panic cause lost: %v", err)
	}
}
