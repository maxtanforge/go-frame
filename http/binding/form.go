package binding

import (
	"context"
	"net/http"

	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/validation"
)

// Form decodes only an application/x-www-form-urlencoded body and validates it.
func Form[Req, Res any](handler func(context.Context, *Req) (*Res, error), options ...Options) (http.Handler, error) {
	return prepare(request.Form, handler, options...)
}

// MultipartOptions separates field DTO limits from upload stream limits.
type MultipartOptions struct {
	Fields Options
	Upload request.MultipartOptions
}

// MultipartDecoder holds immutable DTO rules and upload limits. Per-request
// upload callbacks keep staging and cleanup local to the handler.
type MultipartDecoder[T any] struct {
	fields *decoder[T]
	upload request.MultipartOptions
}

// PrepareMultipart compiles a streaming multipart decoder reusable across requests.
func PrepareMultipart[T any](options ...MultipartOptions) (*MultipartDecoder[T], error) {
	if len(options) > 1 {
		return nil, validation.ErrSchema
	}
	o := MultipartOptions{}
	if len(options) == 1 {
		o = options[0]
	}
	if err := o.Upload.Validate(); err != nil {
		return nil, err
	}
	o.Upload.FileFields = append([]string(nil), o.Upload.FileFields...)
	o.Upload.AllowedMediaTypes = append([]string(nil), o.Upload.AllowedMediaTypes...)
	d, err := newDecoder[T](request.Form, o.Fields)
	if err != nil {
		return nil, err
	}
	return &MultipartDecoder[T]{fields: d, upload: o.Upload}, nil
}

// Decode streams files first, then validates all form fields. The callback owns
// staged file cleanup on every exit; do not commit uploads until Decode succeeds.
func (d *MultipartDecoder[T]) Decode(w http.ResponseWriter, r *http.Request, onFile request.UploadHandler) (T, error) {
	var zero T
	if d == nil || d.fields == nil {
		return zero, validation.ErrSchema
	}
	values, err := request.StreamMultipart(w, r, onFile, d.upload)
	if err != nil {
		return zero, err
	}
	in, states, err := d.fields.input.DecodeFormValues(values)
	return d.fields.validate(r.Context(), in, states, err)
}
