package request

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"

	"github.com/maxtanforge/go-frame/validation"
)

func readForm(w http.ResponseWriter, r *http.Request, limit int64) (url.Values, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		return nil, inputFailure(415)
	}
	if r.Body == nil {
		return nil, inputFailure(400)
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if r.Context().Err() != nil {
		return nil, r.Context().Err()
	}
	if err != nil {
		return nil, bodyFailure(err)
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, inputFailure(400)
	}
	return values, nil
}

func bodyFailure(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		return inputFailure(413)
	}
	return inputFailure(400)
}

// DecodeFormValues binds previously bounded multipart fields to a fresh Form DTO.
// It never reads URL query values or interprets nested key notation.
func (p *Prepared[T]) DecodeFormValues(values url.Values) (T, map[string]validation.FieldState, error) {
	var out T
	states := map[string]validation.FieldState{}
	if p.options.Source != Form {
		return out, states, ErrSchema
	}
	data := make(map[string]any, len(values))
	var size int64
	if len(values) > p.options.MaxFields {
		return out, states, inputFailure(413)
	}
	for k, items := range values {
		if len(items) > p.options.MaxItems {
			return out, states, inputFailure(413)
		}
		size += int64(len(k))
		for _, v := range items {
			size += int64(len(v))
		}
		if size > p.options.MaxBytes {
			return out, states, inputFailure(413)
		}
		data[k] = items
	}
	issues := []validation.Issue{}
	bindPreparedObject(reflect.ValueOf(&out).Elem(), p.fields, data, "", nil, states, &issues, Form)
	if len(issues) > 0 {
		return out, states, validation.NewErrors(issues...)
	}
	return out, states, nil
}
