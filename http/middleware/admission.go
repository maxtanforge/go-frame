package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/maxtanforge/go-frame/concurrency"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/telemetry/logger"
)

// AdmissionEvent observes one admission attempt. Duration includes time waiting
// for capacity; Err is nil when admitted. Events contain no request identifiers.
type AdmissionEvent struct {
	Duration time.Duration
	Err      error
}

// AdmissionOptions controls the admission wait budget and optional observation.
// OnAdmission must be fast and concurrency-safe. Its panic is logged and isolated.
type AdmissionOptions struct {
	WaitTimeout time.Duration
	OnAdmission func(context.Context, AdmissionEvent)
}

// Admission bounds concurrent handler executions and their waiting callers using
// a NewBoundedLimiter. When waiters are allowed, WaitTimeout must be positive;
// otherwise it may be zero. Negative timeouts and unbounded limiters are rejected.
// Capacity exhaustion and local wait expiry return 503. Parent request deadlines
// return 504, and canceled requests receive no response. Admitted handlers retain
// their original context and must cooperate with cancellation. Streaming handlers
// hold admission until they return; mount probes outside this middleware.
func Admission(limiter *concurrency.Limiter, opts AdmissionOptions) (func(http.Handler) http.Handler, error) {
	if limiter == nil {
		return nil, errors.New("middleware: nil admission limiter")
	}
	stats := limiter.Stats()
	if stats.MaxWaiters < 0 || stats.Capacity <= 0 {
		return nil, errors.New("middleware: admission requires a bounded limiter")
	}
	if opts.WaitTimeout < 0 || (stats.MaxWaiters > 0 && opts.WaitTimeout == 0) {
		return nil, errors.New("middleware: waiting admission requires a positive timeout")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := r.Context()
			waitCtx := ctx
			cancel := func() {}
			if opts.WaitTimeout > 0 {
				waitCtx, cancel = context.WithTimeout(ctx, opts.WaitTimeout)
			}
			release, err := limiter.Acquire(waitCtx)
			cancel()
			if release != nil {
				defer release()
			}
			parentErr := admissionContextError(ctx)
			if parentErr != nil {
				err = parentErr
			}
			if opts.OnAdmission != nil {
				observeAdmission(ctx, opts.OnAdmission, AdmissionEvent{Duration: time.Since(start), Err: err})
			}
			if err == nil {
				next.ServeHTTP(w, r)
				return
			}
			if parentErr != nil {
				respond.WriteError(w, r, parentErr)
			} else if errors.Is(err, concurrency.ErrOverloaded) || errors.Is(err, context.DeadlineExceeded) {
				respond.WriteError(w, r, &httperror.Error{Status: http.StatusServiceUnavailable, Message: http.StatusText(http.StatusServiceUnavailable)})
			} else {
				respond.WriteError(w, r, err)
			}
		})
	}, nil
}

func admissionContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func observeAdmission(ctx context.Context, f func(context.Context, AdmissionEvent), event AdmissionEvent) {
	defer func() {
		if p := recover(); p != nil {
			logger.FromContext(ctx).ErrorContext(ctx, "admission observer panic", "panic", p)
		}
	}()
	f(ctx, event)
}
