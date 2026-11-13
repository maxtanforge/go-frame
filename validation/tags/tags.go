// Package tags adapts declarative v-tag validation to safe input errors.
package tags

import (
	"reflect"

	"github.com/maxtanforge/go-frame/validation"
)

// Validator is a convenience for direct values. HTTP registration should use
// Prepare once and retain its immutable plan instead of preparing per request.
type Validator struct{ initialized bool }

// New returns the ready-to-use validator; the zero value is unusable.
func New() *Validator { return &Validator{initialized: true} }

// Validate plans and executes the rules for one value at a time.
func (v *Validator) Validate(value any) error {
	if v == nil || !v.initialized || value == nil {
		return validation.ErrSchema
	}
	p, err := Prepare(reflect.TypeOf(value))
	if err != nil {
		return err
	}
	return p.Validate(value, nil)
}
