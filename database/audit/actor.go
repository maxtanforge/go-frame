// Package audit defines explicit creation and update metadata, independent of
// authentication, persistence, clocks, and transaction policy.
package audit

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalid reports metadata that does not satisfy the Actor contract.
var ErrInvalid = errors.New("audit: invalid metadata")

// Kind classifies the actor recorded in audit fields.
type Kind string

// Actor kinds: an end user, an internal system action, or an explicit anonymous choice.
const (
	Subject   Kind = "subject"
	System    Kind = "system"
	Anonymous Kind = "anonymous"
)

// Actor is an application-provided identity, not proof of authentication.
// Anonymous must be explicitly selected; an unspecified Actor is invalid.
type Actor struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Validate enforces the Kind/ID contract; Anonymous must carry no identifier.
func (a Actor) Validate() error {
	if len(a.Name) > 128 || !utf8.ValidString(a.Name) || strings.TrimSpace(a.Name) != a.Name {
		return ErrInvalid
	}
	for _, r := range a.Name {
		if unicode.IsControl(r) {
			return ErrInvalid
		}
	}
	if a.Kind == Anonymous {
		if a.ID != "" || a.Name != "" {
			return ErrInvalid
		}
		return nil
	}
	if a.Kind != Subject && a.Kind != System {
		return ErrInvalid
	}
	if a.ID == "" || len(a.ID) > 128 || !utf8.ValidString(a.ID) || strings.TrimSpace(a.ID) != a.ID {
		return ErrInvalid
	}
	for _, r := range a.ID {
		if unicode.IsControl(r) {
			return ErrInvalid
		}
	}
	return nil
}
