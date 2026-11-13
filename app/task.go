package app

import (
	"context"
	"errors"
	"sync"
)

// Task adapts a cooperative background function to a one-shot Service. Its
// context is independent of the App startup context and canceled on stop. The
// callback must return after cancellation; Close cannot terminate a goroutine.
type Task struct {
	fn               func(context.Context) error
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	started, stopped bool
	done             chan struct{}
	err              error
}

// NewTask creates a task. A nil callback is rejected.
func NewTask(fn func(context.Context) error) (*Task, error) {
	if fn == nil {
		return nil, errors.New("app: task callback is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Task{fn: fn, ctx: ctx, cancel: cancel, done: make(chan struct{})}, nil
}

// Run calls the function once, unless already stopped. Context cancellation
// returned directly or through single-error wrappers after a stop is normal
// completion. Joined errors and panics are retained.
func (t *Task) Run() error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("app: task Run may only be called once")
	}
	t.started = true
	if t.stopped {
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()
	err := runSafely(func() error { return t.fn(t.ctx) })
	t.mu.Lock()
	// Only a pure cancellation chain is normal completion; retain diagnostics.
	if t.ctx.Err() != nil && pureCancellation(err) {
		err = nil
	}
	t.err = err
	close(t.done)
	t.mu.Unlock()
	t.cancel()
	return err
}
func (t *Task) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.stopped {
		t.stopped = true
		t.cancel()
		if !t.started {
			close(t.done)
		}
	}
}

// Shutdown cancels the callback and waits within ctx for its completion. A later
// call can continue waiting after an earlier budget expired.
func (t *Task) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("app: shutdown context is required")
	}
	t.stop()
	select {
	case <-t.done:
	default:
		select {
		case <-t.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// Close requests cancellation immediately without waiting for the callback.
func (t *Task) Close() error { t.stop(); return nil }

// pureCancellation intentionally does not use errors.Is: a matching leaf in a
// joined error or PanicError must not erase the rest of the diagnostic.
func pureCancellation(err error) bool {
	for err != nil {
		if _, ok := err.(*PanicError); ok { //nolint:errorlint // 手动逐层解包,逐层类型匹配即预期语义
			return false
		}
		if err == context.Canceled { //nolint:errorlint // 手动逐层解包,逐层哨兵匹配即预期语义
			return true
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = wrapped.Unwrap()
	}
	return false
}
