package concurrency

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"
)

// ErrClosed means an executor no longer accepts work.
var ErrClosed = errors.New("concurrency: executor closed")

// ExecutorOptions bounds running and waiting work. Zero concurrency snapshots
// GOMAXPROCS; waiting is disabled by default. Queued work requires a positive
// WaitTimeout. RunTimeout is cooperative and starts after admission.
type ExecutorOptions struct {
	Concurrency int
	MaxWaiters  int
	WaitTimeout time.Duration
	RunTimeout  time.Duration
}

// Executor runs callbacks in caller-owned goroutines under a shared budget.
// It creates no worker pool, retries, detached work or global runtime settings.
// Callbacks must honor context. Do not copy an Executor.
type Executor struct {
	limiter       *Limiter
	opts          ExecutorOptions
	mu            sync.Mutex
	closed        bool
	inflight      int
	done          chan struct{}
	admission     context.Context
	stopAdmission context.CancelFunc
	running       context.Context
	stopRunning   context.CancelFunc
}

// NewExecutor constructs a process-local execution budget without starting work.
func NewExecutor(opts ExecutorOptions) (*Executor, error) {
	if opts.Concurrency < 0 || opts.MaxWaiters < 0 || opts.WaitTimeout < 0 || opts.RunTimeout < 0 || opts.MaxWaiters > 0 && opts.WaitTimeout == 0 {
		return nil, errors.New("concurrency: invalid executor budget")
	}
	if opts.Concurrency == 0 {
		opts.Concurrency = runtime.GOMAXPROCS(0)
	}
	limiter, err := NewBoundedLimiter(opts.Concurrency, opts.MaxWaiters)
	if err != nil {
		return nil, err
	}
	admission, stopAdmission := context.WithCancel(context.Background())
	running, stopRunning := context.WithCancel(context.Background())
	return &Executor{limiter: limiter, opts: opts, done: make(chan struct{}), admission: admission, stopAdmission: stopAdmission, running: running, stopRunning: stopRunning}, nil
}

// Do waits within budget then calls fn synchronously. Panics propagate after
// releasing capacity. Nested Do on the same budget can overload or time out.
func (e *Executor) Do(ctx context.Context, fn func(context.Context) error) error {
	if ctx == nil || fn == nil {
		return errors.New("concurrency: context and callback are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return ErrClosed
	}
	e.inflight++
	e.mu.Unlock()
	defer e.finish()
	waitCtx, cancelWait := context.WithCancel(ctx)
	if e.opts.WaitTimeout > 0 {
		cancelWait()
		waitCtx, cancelWait = context.WithTimeout(ctx, e.opts.WaitTimeout)
	}
	stopWait := context.AfterFunc(e.admission, cancelWait)
	release, err := e.limiter.Acquire(waitCtx)
	stopWait()
	cancelWait()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.admission.Err() != nil {
			return ErrClosed
		}
		return err
	}
	defer release()
	// Close the admission/shutdown race before beginning the callback.
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return ErrClosed
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	if e.opts.RunTimeout > 0 {
		cancelRun()
		runCtx, cancelRun = context.WithTimeout(ctx, e.opts.RunTimeout)
	}
	defer cancelRun()
	stopRun := context.AfterFunc(e.running, cancelRun)
	defer stopRun()
	if err := runCtx.Err(); err != nil {
		return err
	}
	err = fn(runCtx)
	if canceled := runCtx.Err(); canceled != nil {
		if err == nil {
			return canceled
		}
		if errors.Is(err, canceled) {
			return err
		}
		return errors.Join(err, canceled)
	}
	return err
}

func (e *Executor) finish() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inflight--
	if e.closed && e.inflight == 0 {
		close(e.done)
	}
}

func (e *Executor) stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		e.stopAdmission()
		if e.inflight == 0 {
			close(e.done)
		}
	}
}

// Shutdown rejects new work and cancels admission waiters, allowing active work
// to finish. An expired shutdown context cancels active callbacks cooperatively.
// Later calls can continue waiting; no callback is forcibly terminated.
func (e *Executor) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("concurrency: shutdown context is required")
	}
	e.stop()
	defer e.stopRunning()
	select {
	case <-e.done:
		return nil
	default:
	}
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close immediately stops admission and cancels active callbacks without waiting.
func (e *Executor) Close() error { e.stop(); e.stopRunning(); return nil }

// Stats reports capacity, waiting callers and cumulative overload rejections.
func (e *Executor) Stats() LimiterStats { return e.limiter.Stats() }
