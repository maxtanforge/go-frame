// Package validation defines transport-independent, public field diagnostics.
package validation

import (
	"errors"
	"fmt"
)

// ErrInvalid identifies input failures through errors.Is.
var ErrInvalid = errors.New("validation: invalid input")

// ErrSchema identifies invalid validation declarations, not client input.
var ErrSchema = errors.New("validation: invalid schema")

// ErrLimit identifies input that exceeds the validation workload or issue budget.
// No partial validation result is returned. It also matches ErrInvalid.
var ErrLimit = fmt.Errorf("%w: workload limit exceeded", ErrInvalid)

// Rule is a fixed, value-free diagnostic. Custom messages and input values do
// not belong in public field errors.
type Rule string

// Low-level decode rules used by FieldError.Rule.
const (
	RuleSize       Rule = "size"
	RuleFormat     Rule = "format"
	RuleUnknown    Rule = "unknown"
	RuleRequired   Rule = "required"
	RuleRepeated   Rule = "repeated"
	RuleValidation Rule = "validation"
)

// FieldError explicitly opts into public disclosure of a developer-declared
// field name and rule. Never construct Field from user input or internal errors.
// Empty Field denotes a request-wide error. HTTP adapters normalize before use.
type FieldError struct {
	Field string `json:"field"`
	Rule  Rule   `json:"rule"`
}

func (e *FieldError) Error() string {
	if e == nil {
		return ErrInvalid.Error()
	}
	n := Normalize(e)
	return "validation: invalid input (" + n.Field + ": " + string(n.Rule) + ")"
}

func (e *FieldError) Unwrap() error { return ErrInvalid }

// Normalize returns an independent, bounded diagnostic. Unknown errors, invalid
// names or rules become a request-wide validation failure. It never uses Error().
// A valid field has 1..64 ASCII identifier characters (letters, digits, _ or -),
// beginning with a letter or _. An empty field is also valid.
func Normalize(err error) *FieldError {
	fallback := &FieldError{Rule: RuleValidation}
	var field *FieldError
	if !errors.As(err, &field) || field == nil || !validName(field.Field) {
		return fallback
	}
	switch field.Rule {
	case RuleSize, RuleFormat, RuleUnknown, RuleRequired, RuleRepeated, RuleValidation:
		return &FieldError{Field: field.Field, Rule: field.Rule}
	default:
		return fallback
	}
}

func validName(name string) bool {
	if len(name) > 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		if i > 0 && (c == '-' || c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}
