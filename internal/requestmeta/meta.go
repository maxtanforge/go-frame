// Package requestmeta shares bounded observation state across middleware context
// copies. Business identity remains a separate immutable principal value.
package requestmeta

import (
	"context"
	"github.com/maxtanforge/go-frame/auth/principal"
	"maps"
	"sync"
	"time"
)

type key struct{}
type State struct {
	mu             sync.Mutex
	actor          principal.Principal
	set            bool
	finished       time.Time
	params         map[string]string
	paramsTooLarge bool
}

// PublishParams merges named route parameters before/after routing. Internal
// catch-all mount keys are excluded. Bound retained copies to 16 KiB.
func PublishParams(ctx context.Context, keys, values []string) {
	s, ok := ctx.Value(key{}).(*State)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paramsTooLarge {
		return
	}
	for i, key := range keys {
		if key == "" || key == "*" || i >= len(values) {
			continue
		}
		if len(key)+len(values[i]) > 16<<10 {
			s.params, s.paramsTooLarge = nil, true
			return
		}
		if s.params == nil {
			s.params = make(map[string]string)
		}
		s.params[key] = values[i]
		total := 0
		for name, value := range s.params {
			total += len(name) + len(value)
		}
		if total > 16<<10 {
			s.params, s.paramsTooLarge = nil, true
			return
		}
	}
}

func Params(ctx context.Context) (map[string]string, bool) {
	if s, ok := ctx.Value(key{}).(*State); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return maps.Clone(s.params), s.paramsTooLarge
	}
	return nil, false
}

// Finish records the first handler completion before observation/serialization.
func Finish(ctx context.Context, at time.Time) {
	if s, ok := ctx.Value(key{}).(*State); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.finished.IsZero() {
			s.finished = at
		}
	}
}

// Finished supplies the completion shared by operation and access observations.
func Finished(ctx context.Context) (time.Time, bool) {
	if s, ok := ctx.Value(key{}).(*State); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.finished, !s.finished.IsZero()
	}
	return time.Time{}, false
}

func Ensure(ctx context.Context) context.Context {
	if _, ok := ctx.Value(key{}).(*State); ok {
		return ctx
	}
	return context.WithValue(ctx, key{}, &State{})
}
func Publish(ctx context.Context, p principal.Principal) bool {
	s, ok := ctx.Value(key{}).(*State)
	if !ok || p.Validate() != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set {
		return s.actor == p
	}
	s.actor = p
	s.set = true
	return true
}
func Actor(ctx context.Context) (principal.Principal, bool) {
	s, ok := ctx.Value(key{}).(*State)
	if !ok {
		return principal.Principal{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actor, s.set
}
