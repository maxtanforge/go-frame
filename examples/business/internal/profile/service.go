// Package profile defines a profile update use case without transport or storage dependencies.
package profile

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// Business errors are safe to expose; adapters map them to their own protocols.
var (
	ErrInvalid         = errors.New("invalid profile change")
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("profile access denied")
	ErrNotFound        = errors.New("profile not found")
	ErrDependency      = errors.New("permission service unavailable")
)

// Basic contains independently editable profile fields.
type Basic struct {
	DisplayName string
	Age         int
}

// Profile is an independent value snapshot. HasNote distinguishes NULL from empty.
type Profile struct {
	ID      string
	Basic   Basic
	Note    string
	HasNote bool
}

// TextChange distinguishes omission from an explicitly empty string.
type TextChange struct {
	Set   bool
	Value string
}

// IntChange distinguishes omission from an explicitly zero value.
type IntChange struct {
	Set   bool
	Value int
}

// NoteChange either sets a value, clears it to NULL, or leaves it unchanged.
type NoteChange struct {
	Set, Clear bool
	Value      string
}

// BasicChange applies only supplied nested fields.
type BasicChange struct {
	DisplayName TextChange
	Age         IntChange
}

// PatchCommand contains editable fields only; actor and target are separate arguments.
type PatchCommand struct {
	Basic BasicChange
	Note  NoteChange
}

// Authorizer checks every actor-target pair, including self edits.
type Authorizer interface {
	CanEdit(context.Context, string, string) (bool, error)
}

// Repository atomically loads, locks, applies change and persists the snapshot
// with its change record. Callback errors and cancellation discard all writes.
// Implementations must not retain the callback or expose shared mutable values.
type Repository interface {
	Update(context.Context, string, func(*Profile) error) (Profile, error)
}

// Service composes business dependencies explicitly.
type Service struct {
	Repository Repository
	Authorizer Authorizer
}
type dependencyError struct{ cause error }

func (e dependencyError) Error() string   { return ErrDependency.Error() }
func (e dependencyError) Unwrap() []error { return []error{ErrDependency, e.cause} }

// ValidID accepts bounded opaque IDs without whitespace or control characters.
func ValidID(s string) bool {
	if len(s) == 0 || len(s) > 128 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r <= 32 || r == 127 {
			return false
		}
	}
	return true
}
func textValid(s string, maxBytes int) bool {
	return len(s) <= maxBytes && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// Patch authorizes the supplied target before opening the repository transaction.
// Permission reflects the upstream decision at call time, not a distributed lock.
func (s Service) Patch(ctx context.Context, actorID, targetID string, cmd PatchCommand) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if !ValidID(actorID) {
		return Profile{}, ErrUnauthenticated
	}
	if !ValidID(targetID) || (!cmd.Basic.DisplayName.Set && !cmd.Basic.Age.Set && !cmd.Note.Set && !cmd.Note.Clear) || cmd.Note.Set && cmd.Note.Clear {
		return Profile{}, ErrInvalid
	}
	if cmd.Basic.DisplayName.Set && !textValid(cmd.Basic.DisplayName.Value, 100) || cmd.Basic.Age.Set && (cmd.Basic.Age.Value < 0 || cmd.Basic.Age.Value > 150) || cmd.Note.Set && !textValid(cmd.Note.Value, 1000) {
		return Profile{}, ErrInvalid
	}
	allowed, err := s.Authorizer.CanEdit(ctx, actorID, targetID)
	if err != nil {
		return Profile{}, dependencyError{err}
	}
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if !allowed {
		return Profile{}, ErrForbidden
	}
	return s.Repository.Update(ctx, targetID, func(p *Profile) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.ID != targetID {
			return ErrInvalid
		}
		if cmd.Basic.DisplayName.Set {
			p.Basic.DisplayName = cmd.Basic.DisplayName.Value
		}
		if cmd.Basic.Age.Set {
			p.Basic.Age = cmd.Basic.Age.Value
		}
		if cmd.Note.Set {
			p.Note = cmd.Note.Value
			p.HasNote = true
		}
		if cmd.Note.Clear {
			p.Note = ""
			p.HasNote = false
		}
		return nil
	})
}
