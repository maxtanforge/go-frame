// Package concurrency provides bounded, per-process concurrency primitives.
// These limits are not distributed quotas or rate limits. Callbacks must
// cooperate with context cancellation; Go cannot forcibly terminate them.
package concurrency

import (
	"context"
	"errors"
	"sync"
)

// Map applies fn to inputs with at most workers concurrent callbacks and returns
// results in input order. It creates at most min(workers, len(inputs)) goroutines,
// rather than a goroutine per input. The first callback error cancels siblings.
// Map waits for all started callbacks to return, even after cancellation, and
// returns nil results on any error. Parent cancellation takes precedence over
// callback errors. Callbacks must honor their context and must not detach work.
// Callback panics follow normal Go behavior and are not converted into errors.
// ctx and fn must be nonnil, and workers must be positive, even for empty inputs.
// Empty inputs return a nonnil empty slice unless ctx is canceled.
func Map[T, R any](ctx context.Context, inputs []T, workers int, fn func(context.Context, T) (R, error)) ([]R, error) {
	if ctx == nil {
		return nil, errors.New("concurrency: nil context")
	}
	if workers <= 0 {
		return nil, errors.New("concurrency: workers must be positive")
	}
	if fn == nil {
		return nil, errors.New("concurrency: nil callback")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make([]R, len(inputs))
	if len(inputs) == 0 {
		return results, nil
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	next := 0
	var firstErr error
	var once sync.Once
	var wg sync.WaitGroup
	for range min(workers, len(inputs)) {
		wg.Go(func() {
			for {
				mu.Lock()
				if workCtx.Err() != nil || next == len(inputs) {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()
				if workCtx.Err() != nil {
					return
				}
				result, err := fn(workCtx, inputs[i])
				if err != nil {
					once.Do(func() { firstErr = err; cancel() })
					return
				}
				results[i] = result
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
