// Package worker demonstrates bounded, process-local delivery scheduling.
// Queue entries are wakeups for already persisted device events, not durable jobs.
package worker

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/concurrency"
)

// ErrFull rejects a wakeup without allocating a goroutine or discarding SQL state.
var ErrFull = errors.New("device delivery queue is full")

// Result reports an attempted delivery. ID is diagnostic data, not a metric
// label. Errors may contain private dependency diagnostics; do not expose them.
type Result struct {
	ID  string
	Err error
}

// Worker owns a finite queue and one cooperative task. The execution budget is
// borrowed and may be shared by several workers; the composition root closes it.
// Do not copy a Worker. Call Run once through app.Group or an owned goroutine.
type Worker struct {
	mu               sync.Mutex
	queue            chan string
	task             *app.Task
	started, stopped bool
	done             chan struct{}
}

// New does not start work. deliver must honor context; report must be fast and
// concurrency-safe when shared. Delivery failures are reported and left pending
// by the business Service. Panics terminate the task and are retained by app.Task.
func New(capacity int, budget *concurrency.Executor, deliver func(context.Context, string) error, report func(Result)) (*Worker, error) {
	if capacity < 1 || capacity > 1024 || budget == nil || deliver == nil {
		return nil, errors.New("device worker requires a bounded queue, budget and delivery function")
	}
	w := &Worker{queue: make(chan string, capacity), done: make(chan struct{})}
	task, err := app.NewTask(func(ctx context.Context) error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case id, ok := <-w.queue:
				if !ok {
					return nil
				}
				err := budget.Do(ctx, func(ctx context.Context) error { return deliver(ctx, id) })
				if report != nil {
					report(Result{ID: id, Err: err})
				}
				if ctx.Err() != nil {
					if err != nil {
						return err
					}
					return ctx.Err()
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}
	w.task = task
	return w, nil
}

// Submit enqueues a persisted event ID without waiting or starting a goroutine.
// Full/closed means only the wakeup failed; durable acceptance must be reported
// independently. Replay pending IDs on restart and after transient failures.
func (w *Worker) Submit(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		return errors.New("invalid device event ID")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return concurrency.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case w.queue <- id:
		return nil
	default:
		return ErrFull
	}
}

// Run processes queued IDs serially under the shared downstream budget.
func (w *Worker) Run() error {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return errors.New("device worker Run may only be called once")
	}
	if w.stopped {
		w.mu.Unlock()
		return nil
	}
	w.started = true
	w.mu.Unlock()
	defer close(w.done)
	defer w.stopAdmission()
	return w.task.Run()
}

func (w *Worker) stopAdmission() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped {
		w.stopped = true
		close(w.queue)
	}
	return w.started
}

// Shutdown closes admission and drains the finite queue within ctx. If the
// budget expires it cancels active delivery; queued/unacknowledged SQL events
// remain pending. A later call can wait for cooperative cancellation to finish.
// If Run never started, no delivery is attempted during shutdown.
func (w *Worker) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("device worker shutdown context is required")
	}
	if !w.stopAdmission() {
		return w.task.Shutdown(ctx)
	}
	select {
	case <-w.done:
		return w.task.Shutdown(ctx)
	case <-ctx.Done():
		return errors.Join(ctx.Err(), w.task.Shutdown(ctx))
	}
}

// Close stops admission and requests immediate cooperative cancellation.
func (w *Worker) Close() error {
	w.stopAdmission()
	return w.task.Close()
}

var _ app.Service = (*Worker)(nil)
