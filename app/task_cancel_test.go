package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestTaskStopPreservesDiagnostics(t *testing.T) {
	cleanup := errors.New("flush failed")
	for _, mode := range []string{"joined", "panic", "wrapped join", "wrapped cancel"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			done := make(chan error, 1)
			task, err := NewTask(func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				switch mode {
				case "panic":
					panic(context.Canceled)
				case "wrapped cancel":
					return fmt.Errorf("stop: %w", ctx.Err())
				case "wrapped join":
					return fmt.Errorf("stop: %w", errors.Join(ctx.Err(), cleanup))
				default:
					return errors.Join(ctx.Err(), cleanup)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			go func() { done <- task.Run() }()
			waitSignal(t, entered)
			shutdownErr := task.Shutdown(bounded(t))
			runErr := <-done
			for _, got := range []error{shutdownErr, runErr} {
				switch mode {
				case "wrapped cancel":
					if got != nil {
						t.Fatal(got)
					}
				case "panic":
					var pe *PanicError
					if !errors.As(got, &pe) || len(pe.Stack()) == 0 {
						t.Fatalf("lost panic: %v", got)
					}
				default:
					if !errors.Is(got, cleanup) {
						t.Fatalf("lost cleanup error: %v", got)
					}
				}
			}
		})
	}
}
