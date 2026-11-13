package binding

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/maxtanforge/go-frame/validation/tags"
)

// XMLOptions bounds XML request bodies and nesting; zero values use defaults.
type XMLOptions struct {
	Limits     request.XMLOptions
	Validation tags.Options
}

// XMLDecoder combines native XML mapping with prepared value validation.
type XMLDecoder[T any] struct {
	options XMLOptions
	rules   *tags.Prepared
}

// PrepareXML compiles value rules once. XML does not support request defaults,
// optional.Field, nullable or present rules. Diagnostic names use json tags or
// the validator's default names; XMLName should carry json:"-".
func PrepareXML[T any](options ...XMLOptions) (*XMLDecoder[T], error) {
	if len(options) > 1 {
		return nil, validation.ErrSchema
	}
	o := XMLOptions{}
	if len(options) == 1 {
		o = options[0]
	}
	if o.Limits.MaxBytes < 0 || o.Limits.MaxDepth < 0 || o.Limits.MaxNodes < 0 {
		return nil, validation.ErrSchema
	}
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		return nil, validation.ErrSchema
	}
	if err := request.CheckXMLSchema(t); err != nil {
		return nil, errors.Join(validation.ErrSchema, err)
	}
	if err := xmlValueSchema(t, map[reflect.Type]bool{}); err != nil {
		return nil, err
	}
	rules, err := tags.Prepare(t, o.Validation)
	if err != nil {
		return nil, err
	}
	return &XMLDecoder[T]{o, rules}, nil
}
func xmlValueSchema(t reflect.Type, seen map[reflect.Type]bool) error {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.PkgPath() == "github.com/maxtanforge/go-frame/optional" {
		return validation.ErrSchema
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if strings.Split(f.Tag.Get("json"), ",")[0] == "-" {
			continue
		}
		for _, name := range []string{"d", "default", "nullable"} {
			if _, ok := f.Tag.Lookup(name); ok {
				return validation.ErrSchema
			}
		}
		for _, rule := range strings.Split(f.Tag.Get("v"), ",") {
			if rule == "present" {
				return validation.ErrSchema
			}
		}
		if err := xmlValueSchema(f.Type, seen); err != nil {
			return err
		}
	}
	return nil
}

// Decode returns the complete validation error collection; HTTP adaptation
// exposes only its first safe message. Native decode errors stop validation.
func (d *XMLDecoder[T]) Decode(w http.ResponseWriter, r *http.Request) (T, error) {
	out, err := request.DecodeXML[T](w, r, d.options.Limits)
	if err != nil {
		return out, err
	}
	err = d.rules.Validate(&out, nil)
	if e := r.Context().Err(); e != nil {
		return out, e
	}
	if errors.Is(err, tags.ErrLimit) {
		return out, &httperror.Error{Status: 413, Message: "请求参数过多", Err: err}
	}
	return out, customValidationError(err)
}

// XML binds native XML, validates it, and writes an unwrapped XML success.
// Errors retain the standard safe JSON error envelope.
func XML[Req, Res any](handler func(context.Context, *Req) (*Res, error), options ...XMLOptions) (http.Handler, error) {
	if handler == nil {
		return nil, validation.ErrSchema
	}
	d, err := PrepareXML[Req](options...)
	if err != nil {
		return nil, err
	}
	return respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		in, err := d.Decode(w, r)
		if err != nil {
			return nil, err
		}
		out, err := handler(r.Context(), &in)
		if err != nil {
			return nil, err
		}
		return nil, respond.XML(w, r, http.StatusOK, out)
	}), nil
}
