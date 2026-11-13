package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrOverloaded means both admission capacity and the configured waiting
// capacity were unavailable. Callers may reject or retry according to policy.
var ErrOverloaded = errors.New("concurrency: admission overloaded")

// Limiter bounds admissions within one process and starts no goroutines.
// NewBoundedLimiter also bounds caller-owned goroutines waiting in Acquire.
// The zero value is unusable. A Limiter must not be copied after first use.
type Limiter struct {
	slots    chan struct{}
	waiters  chan struct{}
	bounded  bool
	rejected atomic.Uint64
}

// LimiterStats contains independent concurrent-safe observations, not a
// reservation or an atomic view across all fields. MaxWaiters is -1 when unbounded.
type LimiterStats struct {
	Capacity   int
	MaxWaiters int
	InUse      int
	Waiting    int
	Rejected   uint64
}

// NewLimiter bounds active admissions but leaves Acquire waiters unbounded.
// Use NewBoundedLimiter for explicit overload rejection. Capacity must be positive.
func NewLimiter(capacity int) (*Limiter, error) {
	if capacity <= 0 {
		return nil, errors.New("concurrency: capacity must be positive")
	}
	return &Limiter{slots: make(chan struct{}, capacity)}, nil
}

// NewBoundedLimiter bounds active admissions and waiting callers. Zero waiters
// means immediate rejection when active capacity is exhausted. No FIFO guarantee
// is made. Capacity must be positive and maxWaiters nonnegative.
func NewBoundedLimiter(capacity, maxWaiters int) (*Limiter, error) {
	if maxWaiters < 0 {
		return nil, errors.New("concurrency: max waiters must be nonnegative")
	}
	l, err := NewLimiter(capacity)
	if err != nil {
		return nil, err
	}
	l.bounded = true
	l.waiters = make(chan struct{}, maxWaiters)
	return l, nil
}

// Acquire waits for admission or cancellation, or returns ErrOverloaded when a
// bounded limiter cannot admit or wait. A context canceled before admission is
// rejected. Racing cancellation and admission can return either outcome without
// leaking a slot. The successful caller must invoke the idempotent release.
// ctx must be nonnil. Waiting duration is controlled by ctx.
func (l *Limiter) Acquire(ctx context.Context) (release func(), err error) {
	if ctx == nil {
		return nil, errors.New("concurrency: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case l.slots <- struct{}{}:
		return l.admitted(ctx)
	default:
	}
	if l.bounded {
		select {
		case l.waiters <- struct{}{}:
			defer func() { <-l.waiters }()
		default:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			l.rejected.Add(1)
			return nil, ErrOverloaded
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case l.slots <- struct{}{}:
		return l.admitted(ctx)
	}
}

func (l *Limiter) admitted(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		<-l.slots
		return nil, err
	}
	return l.release(), nil
}

// TryAcquire admits immediately or records a capacity rejection. It does not
// join the waiters. On failure release is nil; success must be released.
func (l *Limiter) TryAcquire() (release func(), ok bool) {
	select {
	case l.slots <- struct{}{}:
		return l.release(), true
	default:
		l.rejected.Add(1)
		return nil, false
	}
}

// InUse returns the number of occupied slots at observation time.
func (l *Limiter) InUse() int { return len(l.slots) }

// Stats reports active occupancy, waiting occupancy and cumulative capacity
// rejections. Waiting is reported only for bounded limiters. Canceled and timed
// out acquisitions do not increment Rejected.
func (l *Limiter) Stats() LimiterStats {
	maxWaiters := -1
	if l.bounded {
		maxWaiters = cap(l.waiters)
	}
	return LimiterStats{Capacity: cap(l.slots), MaxWaiters: maxWaiters,
		InUse: len(l.slots), Waiting: len(l.waiters), Rejected: l.rejected.Load()}
}

func (l *Limiter) release() func() {
	var once sync.Once
	return func() { once.Do(func() { <-l.slots }) }
}
