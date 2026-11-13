package categoryhttp

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	httprequest "github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/maxtanforge/go-frame/validation/tags"
)

// CreateUseCase owns parent permissions, name invariants, persistence and the
// transaction. A successful call must return a model with a valid new ID.
type CreateUseCase func(ctx context.Context, actor string, parentID category.ID, name string) (category.Model, error)

type creation struct {
	useCase              CreateUseCase
	pathInput            *httprequest.Prepared[createPath]
	bodyInput            *httprequest.Prepared[createDTO]
	pathRules, bodyRules *tags.Prepared
}

// CreateHandler prepares decoding and validation once at startup. Its explicit
// order records the validated parent before reading the body, then replaces it
// with the new category only after successful creation. It rejects a missing
// active operation recorder; authenticated routing is the mount's responsibility.
func CreateHandler(create CreateUseCase) (http.Handler, error) {
	if create == nil {
		return nil, errors.New("category create use case required")
	}
	pathInput, err := httprequest.Prepare[createPath](httprequest.Options{Source: httprequest.Path})
	if err != nil {
		return nil, err
	}
	pathRules, err := tags.Prepare(reflect.TypeFor[createPath]())
	if err != nil {
		return nil, err
	}
	bodyInput, err := httprequest.Prepare[createDTO](httprequest.Options{Source: httprequest.JSON})
	if err != nil {
		return nil, err
	}
	bodyRules, err := tags.Prepare(reflect.TypeFor[createDTO]())
	if err != nil {
		return nil, err
	}
	h := Handler{creation: &creation{useCase: create, pathInput: pathInput, bodyInput: bodyInput, pathRules: pathRules, bodyRules: bodyRules}}
	return respond.Adapter(h.Create), nil
}

// Create preserves parent audit identity until the new resource is validated.
func (h Handler) Create(w http.ResponseWriter, r *http.Request) (any, error) {
	create := h.creation.useCase
	pathInput, bodyInput := h.creation.pathInput, h.creation.bodyInput
	pathRules, bodyRules := h.creation.pathRules, h.creation.bodyRules
	ctx := r.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, states, err := pathInput.Decode(w, r, map[string]string{"bookId": chi.URLParam(r, "bookId")})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := pathRules.Validate(&path, states); err != nil {
		return nil, err
	}
	parent, err := category.ParseID(path.ParentID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := operationhttp.SetResource(ctx, "book", parent.String()); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, states, err := bodyInput.Decode(w, r, nil)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := bodyRules.Validate(&body, states); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	model, err := create(ctx, actor(ctx), parent, body.Name)
	if canceled := ctx.Err(); canceled != nil {
		return nil, errors.Join(err, canceled)
	}
	if err != nil {
		return nil, httpError(err)
	}
	if !model.ID.Valid() {
		return nil, errors.New("category create returned invalid ID")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := operationhttp.SetResource(ctx, "category", model.ID.String()); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return public(model), nil
}
