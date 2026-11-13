// Package reload provides isolated configuration snapshots and explicit-file watching.
package reload

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	configcore "github.com/maxtanforge/go-frame/config"
)

// ErrRestartRequired identifies changes that cannot be applied to a running service.
var ErrRestartRequired = errors.New("reload: restart required")

// Snapshot is an independent copy of a published configuration revision.
type Snapshot[T any] struct {
	Version uint64
	Value   T
}

// Options defines consumer-owned copying, policy, and bounded in-memory effects.
// Clone must return an independent deep-enough copy and be safe for concurrent use.
// Validate must be pure. Prepare must have no externally visible effects: its
// optional commit must be bounded and cannot fail. Diff, when present, is a pure
// function reporting the consumer's own path vocabulary for what changed between
// two values; the manager returns it from successful updates without interpreting
// it. Callbacks must not panic or reenter the manager. Commit and snapshot
// publication are not a distributed transaction.
type Options[T any] struct {
	Clone    func(T) T
	Validate func(T) error
	Diff     func(current, candidate T) []string
	Prepare  func(current, candidate T) (commit func(), err error)
}

// Manager serializes updates while allowing readers to obtain whole snapshots.
// A Manager must not be copied after first use.
type Manager[T any] struct {
	mu      sync.RWMutex
	current Snapshot[T]
	options Options[T]
	gate    chan struct{}
}

// New validates and copies initial, publishing it at revision 1.
func New[T any](initial T, options Options[T]) (*Manager[T], error) {
	if options.Clone == nil || options.Validate == nil {
		return nil, errors.New("reload: Clone and Validate are required")
	}
	value := options.Clone(initial)
	if err := options.Validate(options.Clone(value)); err != nil {
		return nil, safeError("validation", err)
	}
	return &Manager[T]{current: Snapshot[T]{Version: 1, Value: value}, options: options, gate: make(chan struct{}, 1)}, nil
}

// Snapshot returns a detached value. A nil or zero Manager returns a zero snapshot.
func (m *Manager[T]) Snapshot() Snapshot[T] {
	if m == nil || m.gate == nil {
		return Snapshot[T]{}
	}
	m.mu.RLock()
	s := m.current
	m.mu.RUnlock()
	s.Value = m.options.Clone(s.Value)
	return s
}

// Update publishes candidate only after successful validation and preparation.
// On success it returns the new snapshot plus the changed-path set from the
// optional Options.Diff (nil when Diff is absent). Cancellation before commit
// and all rejected changes retain the old revision and return nil paths.
// On error the returned snapshot is an isolated copy of the current revision.
func (m *Manager[T]) Update(ctx context.Context, candidate T) (Snapshot[T], []string, error) {
	if m == nil || m.gate == nil {
		return Snapshot[T]{}, nil, errors.New("reload: uninitialized manager")
	}
	if ctx == nil {
		return m.Snapshot(), nil, errors.New("reload: nil context")
	}
	select {
	case m.gate <- struct{}{}:
	case <-ctx.Done():
		return m.Snapshot(), nil, ctx.Err()
	}
	defer func() { <-m.gate }()
	if err := ctx.Err(); err != nil {
		return m.Snapshot(), nil, err
	}
	current := m.Snapshot()
	if current.Version == math.MaxUint64 {
		return current, nil, errors.New("reload: version exhausted")
	}
	value := m.options.Clone(candidate)
	if err := m.options.Validate(m.options.Clone(value)); err != nil {
		return m.Snapshot(), nil, safeError("validation", err)
	}
	var changed []string
	if m.options.Diff != nil {
		changed = m.options.Diff(m.options.Clone(current.Value), m.options.Clone(value))
	}
	var commit func()
	if m.options.Prepare != nil {
		var err error
		commit, err = m.options.Prepare(m.options.Clone(current.Value), m.options.Clone(value))
		if err != nil {
			return m.Snapshot(), nil, safeError("preparation", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return m.Snapshot(), nil, err
	}
	if commit != nil {
		commit()
	}
	m.mu.Lock()
	m.current = Snapshot[T]{Version: current.Version + 1, Value: value}
	m.mu.Unlock()
	return m.Snapshot(), changed, nil
}

// safeError copies value-free config diagnostics without retaining callback
// wrappers. As with config.Decode, diagnostic metadata must be schema-owned.
func safeError(stage string, err error) error {
	var diagnostics []error
	var field *configcore.FieldError
	if errors.As(err, &field) && field != nil {
		switch field.Rule {
		case configcore.RuleType, configcore.RuleSize, configcore.RuleFinite, configcore.RuleRequired, configcore.RuleFormat, configcore.RuleRange, configcore.RuleChoice, configcore.RuleRelation, configcore.RuleLength:
			diagnostic := *field
			diagnostics = append(diagnostics, &diagnostic)
		}
	}
	for _, known := range []error{ErrRestartRequired, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			diagnostics = append(diagnostics, known)
		}
	}
	if len(diagnostics) > 0 {
		return fmt.Errorf("reload: %s failed: %w", stage, errors.Join(diagnostics...))
	}
	return fmt.Errorf("reload: %s failed", stage)
}
