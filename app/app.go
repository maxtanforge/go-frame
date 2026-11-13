// Package app manages a service and its dependencies with ordered startup,
// rollback, graceful shutdown and reverse-order cleanup. Callbacks must honor
// their contexts; the library never terminates the process or installs signals.
package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/maxtanforge/go-frame/internal/isnil"
)

// Service is a blocking server with graceful and immediate shutdown support.
// Close must unblock Run. Shutdown must honor its context.
type Service interface {
	Run() error
	Shutdown(context.Context) error
	Close() error
}

// Hook owns a resource. Start follows dependencies, then registration order;
// Stop runs in reverse compiled order, including the hook whose Start failed (which may have partially opened
// a resource). Stop must therefore tolerate partial initialization.
type Hook struct {
	Name string
	// DependsOn names hooks that must start successfully before this hook.
	// Cleanup reverses the compiled startup order.
	DependsOn []string
	Start     func(context.Context) error
	Stop      func(context.Context) error
}

// Options controls lifecycle budgets. Zero uses 30s startup and 15s shutdown.
// OnShutdown runs before draining, e.g. to set readiness to false. It must not block.
// A panic is returned as PanicError after draining and resource cleanup continue.
type Options struct {
	Hooks           []Hook
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
	// ShutdownDelay gives load balancers time to observe OnShutdown readiness
	// changes while the service still accepts residual traffic. It is included
	// in ShutdownTimeout and skipped if the service has already stopped.
	ShutdownDelay time.Duration
	// CleanupReserve reserves part of ShutdownTimeout for resource Stop hooks.
	// Zero disables reservation. Withdrawal and drain share the earlier deadline;
	// hooks may use all time remaining before the total shutdown deadline.
	CleanupReserve time.Duration
	OnShutdown     func()
}

// App runs once. Construct another App to restart a service.
type App struct {
	service Service
	opts    Options
	started atomic.Bool
}

// New validates configuration and takes a snapshot of the hook list.
func New(service Service, opts Options) (*App, error) {
	if isnil.Check(service) {
		return nil, errors.New("app: service is required")
	}
	if opts.StartupTimeout < 0 || opts.ShutdownTimeout < 0 {
		return nil, errors.New("app: timeouts cannot be negative")
	}
	if opts.StartupTimeout == 0 {
		opts.StartupTimeout = 30 * time.Second
	}
	if opts.ShutdownTimeout == 0 {
		opts.ShutdownTimeout = 15 * time.Second
	}
	if opts.ShutdownDelay < 0 || opts.ShutdownDelay >= opts.ShutdownTimeout {
		return nil, errors.New("app: shutdown delay must be nonnegative and less than shutdown timeout")
	}
	// Subtraction avoids overflow when durations are near their maximum value.
	if opts.CleanupReserve < 0 || opts.CleanupReserve >= opts.ShutdownTimeout-opts.ShutdownDelay {
		return nil, errors.New("app: cleanup reserve must be nonnegative and shutdown delay plus cleanup reserve must be less than shutdown timeout")
	}
	hooks, err := compileHooks(opts.Hooks)
	if err != nil {
		return nil, err
	}
	opts.Hooks = hooks
	return &App{service: service, opts: opts}, nil
}

// Run starts dependencies and serves until cancellation or a server error.
// Cancellation after startup is a normal shutdown; startup cancellation is an
// error. Service Run, Shutdown and Close panics become PanicError; shutdown
// failures still attempt Close and reverse resource cleanup.
// Cleanup uses a fresh context so cancellation cannot skip cleanup.
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("app: context is required")
	}
	if !a.started.CompareAndSwap(false, true) {
		return errors.New("app: Run may only be called once")
	}
	startCtx, cancelStart := context.WithTimeout(ctx, a.opts.StartupTimeout)
	defer cancelStart()
	active := 0
	for i, h := range a.opts.Hooks {
		if err := startCtx.Err(); err != nil {
			return errors.Join(err, a.rollback(active))
		}
		active = i + 1
		if h.Start != nil {
			if err := runSafely(func() error { return h.Start(startCtx) }); err != nil {
				return errors.Join(fmt.Errorf("app: start %s: %w", h.Name, err), a.rollback(active))
			}
		}
	}
	if err := startCtx.Err(); err != nil {
		return errors.Join(err, a.rollback(active))
	}
	cancelStart()
	result := make(chan error, 1)
	go func() { result <- runSafely(a.service.Run) }()
	var runErr error
	finished := false
	select {
	case runErr = <-result:
		finished = true
	case <-ctx.Done():
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), a.opts.ShutdownTimeout)
	defer cancelStop()
	drainCtx := stopCtx
	if a.opts.CleanupReserve > 0 {
		deadline, _ := stopCtx.Deadline()
		var cancelDrain context.CancelFunc
		drainCtx, cancelDrain = context.WithDeadline(stopCtx, deadline.Add(-a.opts.CleanupReserve))
		defer cancelDrain()
	}
	var notificationErr error
	if a.opts.OnShutdown != nil {
		if err := runSafely(func() error { a.opts.OnShutdown(); return nil }); err != nil {
			notificationErr = fmt.Errorf("app: shutdown notification: %w", err)
		}
	}
	if !finished && a.opts.ShutdownDelay > 0 {
		timer := time.NewTimer(a.opts.ShutdownDelay)
		select {
		case runErr = <-result:
			finished = true
		case <-timer.C:
		case <-drainCtx.Done():
		}
		timer.Stop()
	}
	drainErr := runSafely(func() error { return a.service.Shutdown(drainCtx) })
	var forceErr error
	if drainErr != nil {
		forceErr = runSafely(a.service.Close)
	}
	if !finished {
		select {
		case runErr = <-result:
		case <-drainCtx.Done():
			if drainErr == nil {
				drainErr = drainCtx.Err()
				forceErr = runSafely(a.service.Close)
			}
			// Prefer an already available result over the simultaneous deadline.
			// A service that ignores Close may outlive the budget; never wait for it
			// indefinitely, but do not discard an error it has already returned.
			select {
			case runErr = <-result:
			default:
			}
		}
	}
	return errors.Join(runErr, notificationErr, drainErr, forceErr, a.stop(stopCtx, active))
}

func (a *App) rollback(active int) error {
	ctx, cancel := context.WithTimeout(context.Background(), a.opts.ShutdownTimeout)
	defer cancel()
	return a.stop(ctx, active)
}

func (a *App) stop(ctx context.Context, active int) error {
	var errs []error
	for i := active - 1; i >= 0; i-- {
		h := a.opts.Hooks[i]
		if h.Stop != nil {
			if err := runSafely(func() error { return h.Stop(ctx) }); err != nil {
				errs = append(errs, fmt.Errorf("app: stop %s: %w", h.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}
