// Package health provides liveness and bounded, named readiness checks.
package health

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Check probes one dependency. Probe must honor cancellation and be concurrency
// safe. A nil probe is an error, never an implicitly healthy dependency.
type Check struct {
	Name  string
	Probe func(context.Context) error
	// Optional failures degrade readiness details without rejecting traffic.
	// This affects runtime readiness, not dependency startup/rollback policy.
	Optional bool
}

// Options configures a single total timeout per readiness request (default 1s).
// Required and optional probes have isolated limits shared across requests;
// their sum bounds total active probes (defaults 4 required + 1 optional).
type Options struct {
	Checks              []Check
	Timeout             time.Duration
	Concurrency         int // Required probes; default 4, shared across requests.
	OptionalConcurrency int // Separate optional probe pool; default 1.
}

// Checker evaluates readiness independently of transport. Probe failures are returned in Report.
// A stuck probe consumes one shared slot until it returns; repeated readiness
// requests cannot create unbounded active probe goroutines.
type Checker struct {
	checks        []Check
	timeout       time.Duration
	slots         chan struct{}
	optionalSlots chan struct{}
	ready         atomic.Bool
}

// New snapshots and validates checks. Empty checks mean no external dependencies.
func New(opts Options) (*Checker, error) {
	if opts.Timeout < 0 || opts.Concurrency < 0 || opts.OptionalConcurrency < 0 {
		return nil, fmt.Errorf("health: timeout and concurrency cannot be negative")
	}
	if opts.Timeout == 0 {
		opts.Timeout = time.Second
	}
	if opts.Concurrency == 0 {
		opts.Concurrency = 4
	}
	if opts.OptionalConcurrency == 0 {
		opts.OptionalConcurrency = 1
	}
	names := map[string]bool{}
	checks := append([]Check(nil), opts.Checks...)
	for _, c := range checks {
		if strings.TrimSpace(c.Name) == "" || names[c.Name] || c.Probe == nil {
			return nil, fmt.Errorf("health: invalid or duplicate check %q", c.Name)
		}
		names[c.Name] = true
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	c := &Checker{checks: checks, timeout: opts.Timeout, slots: make(chan struct{}, opts.Concurrency), optionalSlots: make(chan struct{}, opts.OptionalConcurrency)}
	c.ready.Store(true)
	return c, nil
}

// SetReady controls admission readiness independently of process liveness.
func (c *Checker) SetReady(ready bool) { c.ready.Store(ready) }

// Failure carries probe diagnostics for an explicitly chosen observer. It is never JSON encoded.
type Failure struct {
	Name     string
	Optional bool
	Err      error
}

// Report is a per-call readiness snapshot independent of HTTP or logging.
type Report struct {
	Status   string            `json:"status"`
	Checks   map[string]string `json:"checks"`
	Failures []Failure         `json:"-"`
}

type result struct {
	index int
	err   error
}

// Readiness evaluates checks within one total budget. Each call owns its report.
func (c *Checker) Readiness(parent context.Context) Report {
	if !c.ready.Load() {
		return Report{Status: "draining"}
	}
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	results := make(chan result, len(c.checks))
	var required, optional []int
	for i := range c.checks {
		if c.checks[i].Optional {
			optional = append(optional, i)
		} else {
			required = append(required, i)
		}
	}
	c.startProbes(ctx, required, c.slots, results)
	c.startProbes(ctx, optional, c.optionalSlots, results)
	states := collectResults(ctx, results, len(c.checks))
	checks := make(map[string]string, len(c.checks))
	var failed []string
	var failures []Failure
	degraded := false
	for i, check := range c.checks {
		if states[i] != nil {
			failures = append(failures, Failure{Name: check.Name, Optional: check.Optional, Err: states[i]})
			if check.Optional {
				degraded = true
				checks[check.Name] = "unavailable"

			} else {
				failed = append(failed, check.Name)

			}
		} else {
			checks[check.Name] = "ok"
		}
	}
	if !c.ready.Load() {
		return Report{Status: "draining"}
	}
	if len(failed) > 0 {
		return Report{Status: "unavailable", Checks: checks, Failures: failures}
	}
	status := "ready"
	if degraded {
		status = "degraded"
	}
	return Report{Status: status, Checks: checks, Failures: failures}
}

func collectResults(ctx context.Context, results <-chan result, count int) []error {
	states := make([]error, count)
	for i := range states {
		states[i] = context.DeadlineExceeded
	}
	remaining := count
	for remaining > 0 {
		select {
		case res := <-results:
			states[res.index] = res.err
			remaining--
		case <-ctx.Done():
			// Each probe sends exactly one result. Preserve results already
			// buffered when the budget expires, without waiting for late probes.
			for range min(remaining, len(results)) {
				res := <-results
				states[res.index] = res.err
			}
			return states
		}
	}
	return states
}

// Pools are isolated so optional probes that ignore cancellation cannot consume
// slots needed for required probes. Each pool is shared across readiness calls.
func (c *Checker) startProbes(ctx context.Context, indices []int, slots chan struct{}, results chan<- result) {
	jobs := make(chan int, len(indices))
	for _, index := range indices {
		jobs <- index
	}
	close(jobs)
	for range min(len(indices), cap(slots)) {
		go func() {
			for index := range jobs {
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				if ctx.Err() != nil {
					<-slots
					return
				}
				err := probe(ctx, c.checks[index].Probe)
				<-slots
				results <- result{index: index, err: err}
			}
		}()
	}
}

func probe(ctx context.Context, f func(context.Context) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("health probe panic: %v", p)
		}
	}()
	if err := probeContextError(ctx); err != nil {
		return err
	}
	if err := f(ctx); err != nil {
		return err
	}
	return probeContextError(ctx)
}

// A busy process may not have scheduled the deadline timer yet. Read the
// absolute deadline as well as Err so a late successful callback cannot be
// advertised as healthy during that cancellation-publication window.
func probeContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
