// Package category owns category rules independently of HTTP and persistence.
package category

import (
	"errors"
	"fmt"
	"strconv"
)

var (
	// ErrInvalid reports an invalid command or value.
	ErrInvalid = errors.New("invalid category input")
	// ErrUnauthenticated reports a missing actor.
	ErrUnauthenticated = errors.New("category actor required")
	// ErrForbidden reports an ownership mismatch.
	ErrForbidden = errors.New("category access denied")
	// ErrImmutable reports a protected system category.
	ErrImmutable = errors.New("system category is immutable")
	// ErrNotFound reports a missing category.
	ErrNotFound = errors.New("category not found")
)

// ID is a positive int64. Its zero value is invalid; every use case checks it.
type ID struct{ value int64 }

// NewID constructs a positive numeric ID for persistence callers.
func NewID(value int64) (ID, error) {
	if value <= 0 {
		return ID{}, fmt.Errorf("%w: ID must be positive", ErrInvalid)
	}
	return ID{value: value}, nil
}

// ParseID requires canonical decimal text, without signs or leading zeros.
func ParseID(raw string) (ID, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || strconv.FormatInt(value, 10) != raw {
		return ID{}, fmt.Errorf("%w: noncanonical ID", ErrInvalid)
	}
	return NewID(value)
}

// Valid rejects the invalid zero value.
func (id ID) Valid() bool { return id.value > 0 }

// String returns the canonical decimal representation.
func (id ID) String() string { return strconv.FormatInt(id.value, 10) }

// Int64 supplies the numeric value for SQL/Ent adapters.
func (id ID) Int64() int64 { return id.value }
