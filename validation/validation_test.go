package validation_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/validation"
)

func TestNormalize(t *testing.T) {
	original := &validation.FieldError{Field: "name", Rule: validation.RuleRequired}
	got := validation.Normalize(fmt.Errorf("private: %w", original))
	if *got != *original || got == original || !errors.Is(original, validation.ErrInvalid) {
		t.Fatalf("invalid copy: %#v", got)
	}
	for _, err := range []error{nil, (*validation.FieldError)(nil), errors.New("private"),
		&validation.FieldError{Field: "secret@example.com", Rule: validation.RuleRequired},
		&validation.FieldError{Field: strings.Repeat("a", 65), Rule: validation.RuleRequired},
		&validation.FieldError{Field: "name", Rule: "private"},
	} {
		got := validation.Normalize(err)
		if got.Field != "" || got.Rule != validation.RuleValidation || strings.Contains(got.Error(), "private") {
			t.Fatalf("unsafe error: %#v", got)
		}
	}
}
