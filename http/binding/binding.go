// Package binding composes prepared DTO decoding, validation and typed handlers.
// It is optional: request and respond do not depend on the tag validation engine.
package binding

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/maxtanforge/go-frame/validation/tags"
)

// Options configures decode limits, validation messages, path extraction and
// business callback error mapping. Options are captured when a handler is built.
type Options struct {
	Limits     request.Options
	Validation tags.Options
	PathParams func(*http.Request) map[string]string
	// MapError maps business callback errors only, never decoding or validation
	// errors. Cancellation and deadline errors bypass it. A nil result retains
	// the original error, whose cause is preserved in every mapped result.
	// The mapper is shared across requests; callers must make it concurrency-safe.
	MapError func(error) error
}

func mapCallbackError(ctx context.Context, err error, mapper func(error) error) error {
	if err == nil || mapper == nil || ctx.Err() != nil ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	mapped := mapper(err)
	if mapped == nil {
		return err
	}
	if errors.Is(mapped, err) {
		return mapped
	}
	return errors.Join(mapped, err)
}

// Custom context failures are terminal infrastructure errors, rather than
// public field collections hidden inside a joined error. Preserve explicit
// application/schema/limit policies and retain the original cause for logging.
func customValidationError(err error) error {
	if err == nil || errors.Is(err, validation.ErrSchema) || errors.Is(err, validation.ErrLimit) {
		return err
	}
	var app *httperror.Error
	if errors.As(err, &app) && app != nil {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &httperror.Error{Status: http.StatusInternalServerError, Message: "服务处理异常", Err: err}
	}
	return err
}

// Query binds a flat query-string DTO; defaults apply to absent inputs only.
func Query[Req, Res any](handler func(context.Context, *Req) (*Res, error), options ...Options) (http.Handler, error) {
	return prepare(request.Query, handler, options...)
}

// JSON binds one strict JSON body; unknown fields and duplicate keys are rejected.
func JSON[Req, Res any](handler func(context.Context, *Req) (*Res, error), options ...Options) (http.Handler, error) {
	return prepare(request.JSON, handler, options...)
}

// Path binds router-decoded parameters; Options.PathParams supplies them.
func Path[Req, Res any](handler func(context.Context, *Req) (*Res, error), options ...Options) (http.Handler, error) {
	return prepare(request.Path, handler, options...)
}

type decoder[T any] struct {
	input   *request.Prepared[T]
	rules   *tags.Prepared
	options Options
}

func newDecoder[T any](source request.Source, options ...Options) (*decoder[T], error) {
	if len(options) > 1 {
		return nil, validation.ErrSchema
	}
	o := Options{}
	if len(options) == 1 {
		o = options[0]
	}
	if source == request.Path && o.PathParams == nil {
		return nil, validation.ErrSchema
	}
	o.Limits.Source = source
	if o.Validation.MaxDepth == 0 {
		o.Validation.MaxDepth = o.Limits.MaxDepth
	}
	if o.Validation.MaxFields == 0 {
		o.Validation.MaxFields = o.Limits.MaxFields
	}
	p, err := request.Prepare[T](o.Limits)
	if err != nil {
		return nil, err
	}
	v, err := tags.Prepare(reflect.TypeFor[T](), o.Validation)
	if err != nil {
		return nil, err
	}
	return &decoder[T]{p, v, o}, nil
}
func (d *decoder[T]) decode(w http.ResponseWriter, r *http.Request) (T, error) {
	var params map[string]string
	if d.options.Limits.Source == request.Path {
		params = d.options.PathParams(r)
	}
	out, states, err := d.input.Decode(w, r, params)
	return d.validate(r.Context(), out, states, err)
}

func (d *decoder[T]) validate(ctx context.Context, out T, states map[string]validation.FieldState, err error) (T, error) {
	bound, isBound := err.(*validation.Errors) //nolint:errorlint // 输入来自框架解码器,不含业务包装
	if err != nil && (!isBound || errors.Is(err, validation.ErrSchema)) {
		return out, err
	}
	issues := []validation.Issue{}
	if bound != nil {
		issues = append(issues, bound.Issues()...)
	}
	fieldErr, customErr := d.rules.Collect(&out, states)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	err = fieldErr
	if errors.Is(err, tags.ErrLimit) {
		return out, &httperror.Error{Status: 413, Message: "请求参数过多", Err: err}
	}
	checked, isChecked := err.(*validation.Errors) //nolint:errorlint // 同上:框架解码器输出
	if err != nil && (!isChecked || errors.Is(err, validation.ErrSchema)) {
		return out, err
	}
	if checked != nil {
		issues = append(issues, checked.Issues()...)
	}
	issues = d.input.OrderIssues(issues)
	custom, isCustom := customErr.(*validation.Errors) //nolint:errorlint // 同上:校验引擎输出
	if customErr != nil && (!isCustom || errors.Is(customErr, validation.ErrSchema)) {
		return out, customValidationError(customErr)
	}
	if custom != nil {
		issues = append(issues, custom.Issues()...)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if len(issues) > 0 {
		return out, validation.NewErrors(issues...)
	}
	return out, nil
}
func prepare[Req, Res any](source request.Source, handler func(context.Context, *Req) (*Res, error), options ...Options) (http.Handler, error) {
	if handler == nil {
		return nil, validation.ErrSchema
	}
	d, err := newDecoder[Req](source, options...)
	if err != nil {
		return nil, err
	}
	return respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		in, err := d.decode(w, r)
		if err != nil {
			return nil, err
		}
		out, err := handler(r.Context(), &in)
		return out, mapCallbackError(r.Context(), err, d.options.MapError)
	}), nil
}

// JSONWithPath binds independent path and body DTOs. A failure in one source
// does not turn its values into defaults from another source.
func JSONWithPath[P, B, R any](handler func(context.Context, *P, *B) (*R, error), options ...Options) (http.Handler, error) {
	return preparePair(request.JSON, handler, options...)
}

// PathWithQuery binds independent path and query DTOs, reporting path issues first.
func PathWithQuery[P, Q, R any](handler func(context.Context, *P, *Q) (*R, error), options ...Options) (http.Handler, error) {
	return preparePair(request.Query, handler, options...)
}
func preparePair[P, B, R any](source request.Source, handler func(context.Context, *P, *B) (*R, error), options ...Options) (http.Handler, error) {
	if handler == nil {
		return nil, validation.ErrSchema
	}
	p, err := newDecoder[P](request.Path, options...)
	if err != nil {
		return nil, err
	}
	b, err := newDecoder[B](source, options...)
	if err != nil {
		return nil, err
	}
	return respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		path, other, err := decodePair(p, b, w, r)
		if err != nil {
			return nil, err
		}
		out, err := handler(r.Context(), &path, &other)
		return out, mapCallbackError(r.Context(), err, p.options.MapError)
	}), nil
}

func decodePair[P, B any](p *decoder[P], b *decoder[B], w http.ResponseWriter, r *http.Request) (P, B, error) {
	var other B
	path, pe := p.decode(w, r)
	pissues, isPath := pe.(*validation.Errors) //nolint:errorlint // Framework decoder returns concrete collections.
	if pe != nil && (!isPath || errors.Is(pe, validation.ErrSchema)) {
		return path, other, pe
	}
	other, be := b.decode(w, r)
	bissues, isBody := be.(*validation.Errors) //nolint:errorlint // Framework decoder returns concrete collections.
	if be != nil && (!isBody || errors.Is(be, validation.ErrSchema)) {
		return path, other, be
	}
	var issues []validation.Issue
	if pissues != nil {
		issues = append(issues, pissues.Issues()...)
	}
	if bissues != nil {
		issues = append(issues, bissues.Issues()...)
	}
	if len(issues) > 0 {
		return path, other, validation.NewErrors(issues...)
	}
	return path, other, nil
}
