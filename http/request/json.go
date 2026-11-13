package request

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/validation"
)

// DecodeOptions sets body and structure limits. MaxBytes <= 0 uses 1 MiB;
// zero structure limits use depth 16, items 1000 and fields 64.
type DecodeOptions struct {
	MaxBytes                      int64
	MaxDepth, MaxItems, MaxFields int
}

// Validator is optionally implemented by a decoded value or its pointer.
// Typed validation.FieldError diagnostics are public; other errors produce a
// generic 400 with their details retained only in httperror.Error.Err.
// validation.ErrSchema is preserved as an internal error (HTTP 500).
type Validator interface{ Validate() error }

// DecodeJSON decodes exactly one non-null JSON value, rejecting unknown object fields.
// It accepts application/json and application/*+json. The body is bounded even
// when trailing whitespace or a second value would otherwise exceed the limit.
// At most one DecodeOptions may be supplied; extras are rejected as ErrSchema.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request, options ...DecodeOptions) (T, error) {
	var zero T
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
		return zero, &httperror.Error{Status: 415, Message: "Content-Type must be JSON"}
	}
	if len(options) > 1 {
		return zero, ErrSchema
	}
	limit := int64(1 << 20)
	structure := Options{MaxDepth: 16, MaxItems: 1000, MaxFields: 64}
	if len(options) == 1 {
		o := options[0]
		if o.MaxDepth < 0 || o.MaxItems < 0 || o.MaxFields < 0 {
			return zero, ErrSchema
		}
		if o.MaxDepth > 0 {
			structure.MaxDepth = o.MaxDepth
		}
		if o.MaxItems > 0 {
			structure.MaxItems = o.MaxItems
		}
		if o.MaxFields > 0 {
			structure.MaxFields = o.MaxFields
		}
	}
	if len(options) == 1 && options[0].MaxBytes > 0 {
		limit = options[0].MaxBytes
	}
	if r.Body == nil {
		return zero, &httperror.Error{Status: 400, Message: "Invalid JSON body"}
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	raw, err := io.ReadAll(body)
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			return zero, &httperror.Error{Status: 413, Message: "Request body too large", Err: err}
		}
		return zero, &httperror.Error{Status: 400, Message: "Invalid JSON body", Err: err}
	}
	if !utf8.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return zero, &httperror.Error{Status: 400, Message: "Invalid JSON body"}
	}
	// Share the prepared decoder's syntax/resource checks while leaving typed
	// decoding to encoding/json (including scalar and custom unmarshaler support).
	preflight := json.NewDecoder(bytes.NewReader(raw))
	preflight.UseNumber()
	if _, err = readPreparedJSON(preflight, 0, structure); err != nil {
		return zero, err
	}
	if _, err = preflight.Token(); !errors.Is(err, io.EOF) {
		return zero, &httperror.Error{Status: 400, Message: "Expected one JSON value", Err: err}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var value T
	if err = dec.Decode(&value); err != nil {
		return zero, &httperror.Error{Status: 400, Message: "Invalid JSON body", Err: err}
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return zero, &httperror.Error{Status: 400, Message: "Expected one JSON value", Err: err}
	}
	err = nil
	if validator, ok := any(&value).(Validator); ok {
		err = validator.Validate()
	} else if validator, ok := any(value).(Validator); ok {
		err = validator.Validate()
	}
	if err != nil {
		if errors.Is(err, validation.ErrSchema) || errors.Is(err, ErrSchema) {
			return zero, err
		}
		var field *validation.FieldError
		var app *httperror.Error
		// Preserve deadline precedence and keep httperror.Error.Err private even when
		// either error chain also contains a public diagnostic type.
		if !errors.Is(err, context.DeadlineExceeded) && !errors.As(err, &app) && errors.As(err, &field) && field != nil {
			return zero, validation.Normalize(err)
		}
		return zero, &httperror.Error{Status: 400, Message: "Invalid request data", Err: err}
	}
	return value, nil
}
