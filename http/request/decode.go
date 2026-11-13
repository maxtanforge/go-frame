// Package request binds explicit, flat query and path DTOs without a router dependency.
package request

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"strconv"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/validation"
)

var (
	// ErrSchema reports a developer-side DTO declaration error, mapped to 500.
	ErrSchema   error = invalidDTOSchema{}
	namePattern       = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
)

// invalidDTOSchema retains the request diagnostic while sharing the
// transport-independent declaration-error category with validation.
type invalidDTOSchema struct{}

func (invalidDTOSchema) Error() string { return "request: invalid DTO schema" }
func (invalidDTOSchema) Unwrap() error { return validation.ErrSchema }

// DecodeQuery applies absent-only defaults, then calls Validate if implemented.
// Every field must have an explicit query tag. Supported fields are strings,
// booleans and integers (including named types); nested/pointer/slice fields are
// deliberately unsupported. Empty, repeated and unknown values are rejected.
// The raw query is limited to 4096 bytes and the DTO to 64 fields; size
// overruns map to HTTP 413 like every bounded input path.
func DecodeQuery[T any](raw string, defaults T) (T, error) {
	var zero T
	if len(raw) > 4096 {
		return zero, inputFailure(413)
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return zero, invalid("", validation.RuleFormat)
	}
	return decode(values, defaults, "query", false)
}

// DecodePath binds already-decoded router parameters. All declared path fields
// are required. Pass only the parameters belonging to this DTO; no percent
// decoding is performed a second time. Input is limited to 4096 combined bytes
// and 64 parameters; size overruns map to HTTP 413.
func DecodePath[T any](params map[string]string) (T, error) {
	var zero T
	if len(params) > 64 {
		return zero, inputFailure(413)
	}
	values := make(url.Values, len(params))
	size := 0
	for key, value := range params {
		if len(key) > 4096 || len(value) > 4096 {
			return zero, inputFailure(413)
		}
		size += len(key) + len(value)
		if size > 4096 {
			return zero, inputFailure(413)
		}
		values[key] = []string{value}
	}
	return decode(values, zero, "path", true)
}

func invalid(field string, rule validation.Rule) error {
	return &validation.FieldError{Field: field, Rule: rule}
}

func decode[T any](values url.Values, defaults T, tag string, required bool) (T, error) {
	var zero T
	original := reflect.ValueOf(defaults)
	if !original.IsValid() || original.Kind() != reflect.Struct || original.NumField() > 64 {
		return zero, ErrSchema
	}
	typ := original.Type()
	names := make(map[string]int, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name := f.Tag.Get(tag)
		if f.PkgPath != "" || !namePattern.MatchString(name) || len(name) > 64 {
			return zero, ErrSchema
		}
		if _, ok := names[name]; ok {
			return zero, ErrSchema
		}
		switch f.Type.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		default:
			return zero, ErrSchema
		}
		names[name] = i
	}
	for name := range values {
		if _, ok := names[name]; !ok {
			return zero, invalid("", validation.RuleUnknown)
		}
	}
	dst := reflect.New(typ)
	dst.Elem().Set(original)
	// Declaration order gives deterministic diagnostics irrespective of map order.
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Tag.Get(tag)
		items, ok := values[name]
		if !ok {
			if required {
				return zero, invalid(name, validation.RuleRequired)
			}
			continue
		}
		if len(items) != 1 {
			return zero, invalid(name, validation.RuleRepeated)
		}
		raw := items[0]
		if raw == "" {
			return zero, invalid(name, validation.RuleRequired)
		}
		field := dst.Elem().Field(i)
		if !assign(field, raw) {
			return zero, invalid(name, validation.RuleFormat)
		}
	}
	if v, ok := dst.Interface().(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			if errors.Is(err, validation.ErrSchema) || errors.Is(err, ErrSchema) {
				return zero, err
			}
			var app *httperror.Error
			// Match DecodeJSON: retain timeout precedence and never promote an
			// application error's private cause to a public field diagnostic.
			if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &app) {
				return zero, &httperror.Error{Status: 400, Message: "Invalid request data", Err: err}
			}
			field := validation.Normalize(err)
			if _, declared := names[field.Field]; field.Field != "" && !declared {
				field = validation.Normalize(nil)
			}
			return zero, field
		}
	}
	return dst.Elem().Interface().(T), nil
}

func assign(field reflect.Value, raw string) bool {
	if field.Type() == reflect.TypeFor[id.ID]() {
		n, err := id.Parse(raw)
		if err != nil {
			return false
		}
		field.SetInt(n.Int64())
		return true
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
		return true
	case reflect.Bool:
		if raw != "true" && raw != "false" {
			return false
		}
		field.SetBool(raw == "true")
		return true
	}
	// Decimal only, no leading plus, whitespace, fraction or exponent.
	digits := raw
	signed := field.Kind() >= reflect.Int && field.Kind() <= reflect.Int64
	if signed && raw[0] == '-' {
		digits = raw[1:]
	}
	if digits == "" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	if signed {
		n, e := strconv.ParseInt(raw, 10, field.Type().Bits())
		if e != nil {
			return false
		}
		field.SetInt(n)
	} else {
		n, e := strconv.ParseUint(raw, 10, field.Type().Bits())
		if e != nil {
			return false
		}
		field.SetUint(n)
	}
	return true
}
