// Package pagination defines protocol-independent, bounded offset pagination.
package pagination

import (
	"errors"
	"math"
)

// ErrInvalid reports invalid pagination or sorting without echoing input.
var ErrInvalid = errors.New("pagination: invalid parameters")

// Limits are an application policy, not values supplied by a request.
type Limits struct{ MaxSize, MaxOffset int64 }

// DefaultLimits caps page size at 100 and offset at 1,000,000.
func DefaultLimits() Limits { return Limits{MaxSize: 100, MaxOffset: 1_000_000} }

// Window can only be constructed through NewWindow. Its zero value is invalid.
type Window struct{ current, size, offset int64 }

// NewWindow requires explicit positive values. Protocol adapters apply defaults only
// when inputs are absent; explicit zero or negative values must not be defaulted.
// MaxSize must leave room for the extra-row probe. MaxOffset is inclusive.
func NewWindow(current, size int64, limits Limits) (Window, error) {
	if current < 1 || size < 1 || limits.MaxSize < 1 || limits.MaxSize == math.MaxInt64 || limits.MaxOffset < 0 || size > limits.MaxSize {
		return Window{}, ErrInvalid
	}
	if current-1 > limits.MaxOffset/size {
		return Window{}, ErrInvalid
	}
	return Window{current: current, size: size, offset: (current - 1) * size}, nil
}

// Current is the 1-based page number.
func (w Window) Current() int64 { return w.current }

// Size is the page size actually used for this query.
func (w Window) Size() int64 { return w.size }

// Offset is the zero-based row offset derived from Current and Size.
func (w Window) Offset() int64 { return w.offset }

// FetchSize includes a single extra row to detect a following page.
func (w Window) FetchSize() int64 { return w.size + 1 }

// Validate re-checks invariants; every Window is valid by construction.
func (w Window) Validate() error {
	if w.current < 1 || w.size < 1 || w.size == math.MaxInt64 || w.offset < 0 || w.current-1 > math.MaxInt64/w.size || (w.current-1)*w.size != w.offset {
		return ErrInvalid
	}
	return nil
}

// Sort names a public field alias, never a SQL expression.
type Sort struct {
	Field string
	Desc  bool
}

// ValidateSort accepts up to eight distinct declared aliases. Empty sorting is
// valid; the storage adapter supplies the application's unique tie-breaker.
func ValidateSort(sorts []Sort, allowed []string) error {
	if len(sorts) > 8 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(sorts))
	for _, s := range sorts {
		if s.Field == "" || seen[s.Field] {
			return ErrInvalid
		}
		found := false
		for _, field := range allowed {
			if s.Field == field {
				found = true
				break
			}
		}
		if !found {
			return ErrInvalid
		}
		seen[s.Field] = true
	}
	return nil
}
