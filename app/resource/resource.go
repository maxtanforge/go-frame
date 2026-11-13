// Package resource adapts explicitly owned dependencies to app lifecycle hooks.
// It is an optional composition layer, not a service locator.
package resource

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/internal/isnil"
)

// Options defines how an application-owned dependency is opened, probed and closed.
// Open and Probe must honor context. Probe is optional; when omitted, readiness
// reflects lifecycle state only. Close is required and receives the Stop context,
// including when it is already canceled. Callbacks own their context behavior.
// Probe must support concurrent calls and concurrent Close.
type Options[T any] struct {
	Name  string
	Open  func(context.Context) (T, error)
	Probe func(context.Context, T) error
	Close func(context.Context, T) error
}

type state uint8

const (
	created state = iota
	starting
	running
	stopped
)

// Resource owns one lazily constructed dependency. Do not copy a Resource.
// Start and Stop serialize: Stop waits for an in-progress Start, so factories
// and startup probes must honor context. Lifecycle callbacks must not call Start
// or Stop on this same resource. Value and Probe may be called concurrently.
type Resource[T any] struct {
	name      string
	open      func(context.Context) (T, error)
	probe     func(context.Context, T) error
	close     func(context.Context, T) error
	lifecycle sync.Mutex
	mu        sync.RWMutex
	state     state
	value     T
	acquired  bool
	closeErr  error
}

// New validates configuration without opening or probing the dependency.
func New[T any](opts Options[T]) (*Resource[T], error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, errors.New("resource: name is required")
	}
	if opts.Open == nil {
		return nil, errors.New("resource: factory is required")
	}
	if opts.Close == nil {
		return nil, errors.New("resource: close is required")
	}
	return &Resource[T]{name: opts.Name, open: opts.Open, probe: opts.Probe, close: opts.Close}, nil
}

// Name returns the name used by both lifecycle and readiness adapters.
func (r *Resource[T]) Name() string { return r.name }

// Hook returns the one-shot lifecycle adapter. App owns rollback on any startup
// error; even a nonnil value returned with a factory error is retained for Stop.
func (r *Resource[T]) Hook() app.Hook { return app.Hook{Name: r.name, Start: r.start, Stop: r.stop} }

// Value returns the dependency only after successful startup and before Stop.
// A returned value is borrowed, not a lease: callers must not close it and must
// finish their work before shutdown.
func (r *Resource[T]) Value() (T, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state != running {
		var zero T
		return zero, fmt.Errorf("resource %s: not ready", r.name)
	}
	return r.value, nil
}

// Probe checks readiness without constructing the dependency. An optional probe
// runs only while ready, and lifecycle state is checked again before success.
func (r *Resource[T]) Probe(ctx context.Context) error {
	v, err := r.Value()
	if err != nil {
		return err
	}
	if r.probe != nil {
		if err := r.probe(ctx, v); err != nil {
			return fmt.Errorf("resource %s: probe: %w", r.name, err)
		}
	}
	_, err = r.Value()
	return err
}

func (r *Resource[T]) start(ctx context.Context) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	r.mu.Lock()
	if r.state != created {
		r.mu.Unlock()
		return fmt.Errorf("resource %s: start may only be called once", r.name)
	}
	r.state = starting
	r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resource %s: startup: %w", r.name, err)
	}
	v, err := r.open(ctx)
	r.mu.Lock()
	r.value = v
	r.acquired = true
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("resource %s: open: %w", r.name, err)
	}
	if isnil.Check(v) {
		return fmt.Errorf("resource %s: factory returned nil", r.name)
	}
	if r.probe != nil {
		if err := r.probe(ctx, v); err != nil {
			return fmt.Errorf("resource %s: startup probe: %w", r.name, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resource %s: startup: %w", r.name, err)
	}
	r.mu.Lock()
	r.state = running
	r.mu.Unlock()
	return nil
}

func (r *Resource[T]) stop(ctx context.Context) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	r.mu.Lock()
	if r.state == stopped {
		err := r.closeErr
		r.mu.Unlock()
		return err
	}
	r.state = stopped
	v := r.value
	acquired := r.acquired
	var zero T
	r.value = zero
	r.mu.Unlock()
	var err error
	if acquired && !isnil.Check(v) {
		if cause := r.close(ctx, v); cause != nil {
			err = fmt.Errorf("resource %s: close: %w", r.name, cause)
		}
	}
	r.mu.Lock()
	r.closeErr = err
	r.mu.Unlock()
	return err
}
