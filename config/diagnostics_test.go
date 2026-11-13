package config

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

type diagnosticSettings struct {
	Token   string `config:"token" env:"TOKEN" secret:"true"`
	Failure error  `config:"-"`
}

func (c diagnosticSettings) Validate() error { return c.Failure }

func TestValidationDiagnosticUsesWinningSource(t *testing.T) {
	_, err := Decode(diagnosticSettings{Failure: Invalid("token", RuleRequired)},
		Source{Name: "yaml", Values: map[string]string{"token": "old-secret"}},
		Source{Name: "environment", Values: map[string]string{"token": "new-secret"}})
	var detail *FieldError
	if !errors.As(err, &detail) {
		t.Fatalf("missing diagnostic: %v", err)
	}
	if detail.Path != "token" || detail.Env != "TOKEN" || detail.Source != "environment" || detail.Rule != RuleRequired || detail.Expected != "string" {
		t.Fatalf("incorrect diagnostic: %+v", detail)
	}
	if strings.Contains(fmt.Sprintf("%+v", detail), "secret") || errors.Unwrap(err) != nil {
		t.Fatal("diagnostic retained secret or cause")
	}
}

func TestValidationDiagnosticRejectsUntrustedMetadata(t *testing.T) {
	for _, input := range []error{
		errors.New("raw-secret"),
		Invalid("raw-secret", RuleRequired),
		Invalid("token", Rule("raw-secret")),
		fmt.Errorf("raw-secret: %w", Invalid("token", RuleRequired)),
		PrefixValidation("raw-secret", Invalid("token", RuleRequired)),
	} {
		_, err := Decode(diagnosticSettings{Failure: input})
		if err == nil || err.Error() != "config: validation failed" || errors.Unwrap(err) != nil {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

type nestedDiagnostic struct {
	Business diagnosticSettings `config:"business"`
}

func (c nestedDiagnostic) Validate() error {
	return PrefixValidation("business", c.Business.Validate())
}

func TestValidationDiagnosticPrefixAndDefault(t *testing.T) {
	_, err := Decode(nestedDiagnostic{Business: diagnosticSettings{Failure: Invalid("token", RuleFormat)}})
	var detail *FieldError
	if !errors.As(err, &detail) || detail.Path != "business.token" || detail.Source != "defaults" {
		t.Fatalf("nested default diagnostic: %v", err)
	}
	if PrefixValidation("business", nil) != nil {
		t.Fatal("nil validation was not preserved")
	}
}

func TestConversionDiagnostic(t *testing.T) {
	_, err := Decode(struct {
		Count int `env:"COUNT"`
	}{}, Source{Name: "cli", Values: map[string]string{"count": "raw-secret"}})
	var detail *FieldError
	if !errors.As(err, &detail) || detail.Rule != RuleType || detail.Path != "count" || detail.Env != "COUNT" || detail.Expected != "int" || detail.Source != "cli" {
		t.Fatalf("conversion diagnostic: %v", err)
	}
	if strings.Contains(err.Error(), "raw-secret") || errors.Unwrap(err) != nil {
		t.Fatal("conversion leaked input")
	}
}

func TestDiagnosticSizeFiniteAndRejectedSource(t *testing.T) {
	_, sizeErr := Decode(struct{ Token string }{}, Source{Name: "yaml", Values: map[string]string{"token": strings.Repeat("s", maxValueBytes+1)}})
	_, finiteErr := Decode(struct{ Number float64 }{Number: math.NaN()})
	_, typeErr := Decode(struct{ Number int }{}, Source{Name: "yaml", Values: map[string]string{"number": "secret"}}, Source{Name: "cli", Values: map[string]string{"number": "1"}})
	for _, tc := range []struct {
		err    error
		rule   Rule
		source string
	}{{sizeErr, RuleSize, "yaml"}, {finiteErr, RuleFinite, "defaults"}, {typeErr, RuleType, "yaml"}} {
		var detail *FieldError
		if !errors.As(tc.err, &detail) || detail.Rule != tc.rule || detail.Source != tc.source {
			t.Fatalf("incorrect failure source/category: %v", tc.err)
		}
	}
}

func TestDiagnosticRejectsNilAndForgedDiagnostic(t *testing.T) {
	var nilViolation *violation
	for _, failure := range []error{nilViolation, &FieldError{Path: "raw-secret"}} {
		_, err := Decode(diagnosticSettings{Failure: failure})
		if err == nil || err.Error() != "config: validation failed" {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}
