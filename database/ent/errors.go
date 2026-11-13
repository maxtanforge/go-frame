package entdb

import (
	"context"
	"errors"
)

// ErrorKind is a transport-independent error category. Classification does not
// replace the original error; callers retain it for errors.Is and errors.As.
type ErrorKind uint8

// Classification kinds produced by ErrorClassifier, coarse enough to map
// onto public error semantics without leaking driver details.
const (
	ErrorNone ErrorKind = iota
	ErrorUnknown
	ErrorNotFound
	ErrorConstraint
	ErrorValidation
	ErrorUniqueConstraint
	ErrorCanceled
	ErrorDeadlineExceeded
)

// ErrorClassifier adapts generated Ent error checks without importing a
// business-generated client. Nil predicates are ignored. Predicates should
// support wrapped errors, as Ent's generated checks do.
//
// IsUniqueConstraint must recognize a specific unique constraint violation;
// IsConstraint alone never implies uniqueness. No error text is parsed.
type ErrorClassifier struct {
	IsNotFound         func(error) bool
	IsConstraint       func(error) bool
	IsValidation       func(error) bool
	IsUniqueConstraint func(error) bool
}

// Classify gives cancellation and deadlines priority over application checks.
// Unique constraints precede general constraints; otherwise the order is not
// found, constraint, validation. An unrecognized error is ErrorUnknown.
func (c ErrorClassifier) Classify(err error) ErrorKind {
	if err == nil {
		return ErrorNone
	}
	if errors.Is(err, context.Canceled) {
		return ErrorCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorDeadlineExceeded
	}
	if c.IsUniqueConstraint != nil && c.IsUniqueConstraint(err) {
		return ErrorUniqueConstraint
	}
	if c.IsNotFound != nil && c.IsNotFound(err) {
		return ErrorNotFound
	}
	if c.IsConstraint != nil && c.IsConstraint(err) {
		return ErrorConstraint
	}
	if c.IsValidation != nil && c.IsValidation(err) {
		return ErrorValidation
	}
	return ErrorUnknown
}
