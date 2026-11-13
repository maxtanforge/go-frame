package config

import "fmt"

// Rule identifies a fixed validation category, never an input value or message.
type Rule string

// Diagnostic rules attached to FieldError for failed environment loading.
const (
	RuleType     Rule = "type"
	RuleSize     Rule = "size"
	RuleFinite   Rule = "finite"
	RuleRequired Rule = "required"
	RuleFormat   Rule = "format"
	RuleRange    Rule = "range"
	RuleChoice   Rule = "choice"
	RuleRelation Rule = "relation"
	RuleLength   Rule = "length"
)

func (r Rule) known() bool {
	switch r {
	case RuleType, RuleSize, RuleFinite, RuleRequired, RuleFormat, RuleRange, RuleChoice, RuleRelation, RuleLength:
		return true
	}
	return false
}

// FieldError is a value-free diagnostic produced by Decode. Metadata comes from
// the declared schema and sources. No original error is retained or unwrapped.
type FieldError struct {
	Path, Env, Source, Expected string
	Rule                        Rule
}

func (e *FieldError) Error() string {
	if e == nil {
		return "config: validation failed"
	}
	field := e.Path
	if e.Env != "" {
		field += " (" + e.Env + ")"
	}
	return fmt.Sprintf("config: %s: source %s: invalid %s; rule %s", field, e.Source, e.Expected, e.Rule)
}

type violation struct {
	path    string
	rule    Rule
	message string // Only built-in validators supply fixed, value-free text.
}

func (v *violation) Error() string {
	if v == nil || v.message == "" {
		return "config: validation failed"
	}
	return v.message
}

// Invalid reports one constraint failure from a consumer's Validate method.
// Path must be a declared leaf path and rule a known constant. Decode checks
// both before exposing them. Do not use configuration values as metadata.
func Invalid(path string, rule Rule) error { return invalidField(path, rule, "") }

func invalidField(path string, rule Rule, message string) error {
	return &violation{path: path, rule: rule, message: message}
}

// PrefixValidation composes nested validators. Nil stays nil; ordinary errors
// remain ordinary errors and Decode will hide their text. Arbitrarily wrapped
// errors are deliberately not traversed; use this helper before returning.
func PrefixValidation(prefix string, err error) error {
	if err == nil {
		return nil
	}
	v, ok := err.(*violation) //nolint:errorlint // 契约:刻意不遍历包装错误,PrefixValidation 须在包装前调用
	if !ok || v == nil {
		return err
	}
	if len(prefix) > 4096 || len(v.path) > 4096 {
		return Invalid("", RuleRequired)
	}
	path := v.path
	if prefix != "" {
		path = prefix + "." + path
	}
	return invalidField(path, v.rule, v.message)
}

func validationDiagnostic(err error, fields []fieldSpec, entries []Entry, expected func(int) string) error {
	v, ok := err.(*violation) //nolint:errorlint // 同上:只识别加载器自身构造的未包装 violation
	if ok && v != nil && v.rule.known() {
		for i, f := range fields {
			if f.path == v.path {
				return &FieldError{Path: f.path, Env: f.env, Source: entries[i].Source, Expected: expected(i), Rule: v.rule}
			}
		}
	}
	return &validationFailure{}
}

type validationFailure struct{}

func (*validationFailure) Error() string { return "config: validation failed" }
