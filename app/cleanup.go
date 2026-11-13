package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Cleanup owns callbacks acquired during composition. Its zero value is ready
// for use. Attach Stop to the composition Hook before startup so partially
// initialized resources are also released on rollback. Do not copy after use.
// It provides no resource lookup; dependencies remain explicitly injected.
type Cleanup struct {
	mu      sync.Mutex
	entries []cleanupEntry
	done    chan struct{}
	err     error
}

type cleanupEntry struct {
	name string
	stop func(context.Context) error
}

// Add transfers cleanup ownership on success. Names must be nonempty and unique.
// Registration freezes when Stop begins. Rejected resources remain caller-owned.
func (c *Cleanup) Add(name string, stop func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done != nil {
		return errors.New("app: cleanup registration is closed")
	}
	if strings.TrimSpace(name) == "" || stop == nil {
		return errors.New("app: cleanup requires a name and stop callback")
	}
	for _, entry := range c.entries {
		if entry.name == name {
			return fmt.Errorf("app: duplicate cleanup name %q", name)
		}
	}
	c.entries = append(c.entries, cleanupEntry{name: name, stop: stop})
	return nil
}

// Stop invokes callbacks once in reverse registration order, joining named
// errors and PanicError diagnostics without skipping remaining callbacks.
// The first caller executes callbacks synchronously with its context, including
// when canceled; callbacks must honor that budget. Later callers wait within
// their own contexts and receive the cached result after completion. Callbacks
// must not call Stop on this Cleanup. A nil context is rejected without freezing
// registration. The budget cannot forcibly terminate an uncooperative callback.
func (c *Cleanup) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("app: cleanup context is required")
	}
	c.mu.Lock()
	if c.done != nil {
		done := c.done
		c.mu.Unlock()
		select {
		case <-done:
			return c.err
		default:
		}
		select {
		case <-done:
			return c.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.done = make(chan struct{})
	entries := c.entries
	c.entries = nil
	c.mu.Unlock()
	var errs []error
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if err := runSafely(func() error { return entry.stop(ctx) }); err != nil {
			errs = append(errs, fmt.Errorf("app: cleanup %s: %w", entry.name, err))
		}
	}
	c.err = errors.Join(errs...)
	close(c.done)
	return c.err
}
