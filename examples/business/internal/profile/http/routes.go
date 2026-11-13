// Package profilehttp maps authenticated HTTP requests to the profile use case.
package profilehttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/examples/business/internal/profile"
	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/optional"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

type targetPath struct {
	ID string `path:"id" v:"present"`
}
type basicDTO struct {
	DisplayName optional.Field[string] `json:"display_name"`
	Age         optional.Field[int]    `json:"age"`
}
type patchDTO struct {
	Basic optional.Field[basicDTO] `json:"basic"`
	Note  optional.Field[string]   `json:"note" nullable:"true"`
}
type basicVO struct {
	DisplayName string `json:"display_name"`
	Age         int    `json:"age"`
}
type profileVO struct {
	ID    string  `json:"id"`
	Basic basicVO `json:"basic"`
	Note  *string `json:"note"`
}

func (p patchDTO) command() profile.PatchCommand {
	b := p.Basic.Value
	return profile.PatchCommand{Basic: profile.BasicChange{DisplayName: profile.TextChange{Set: b.DisplayName.Present, Value: b.DisplayName.Value}, Age: profile.IntChange{Set: b.Age.Present, Value: b.Age.Value}}, Note: profile.NoteChange{Set: p.Note.Present && !p.Note.Null, Clear: p.Note.Present && p.Note.Null, Value: p.Note.Value}}
}

// Routes prepares a protected PATCH route; compose it with router.NewAuthenticated.
// The authenticated principal is the sole source of actor identity.
func Routes(service profile.Service) ([]router.Route, error) {
	if service.Repository == nil || service.Authorizer == nil {
		return nil, errors.New("profile dependencies required")
	}
	patch := func(ctx context.Context, path *targetPath, body *patchDTO) (*profileVO, error) {
		actor, _ := principal.FromContext(ctx)
		m, err := service.Patch(ctx, actor.ID, path.ID, body.command())
		if err != nil {
			// Map here because binding.MapError deliberately bypasses context
			// causes. A child dependency deadline still needs a safe 502 while
			// respond owns cancellation/deadline of the incoming request itself.
			return nil, httpError(err)
		}
		v := &profileVO{ID: m.ID, Basic: basicVO{m.Basic.DisplayName, m.Basic.Age}}
		if m.HasNote {
			v.Note = &m.Note
		}
		return v, nil
	}
	opts := binding.Options{PathParams: func(r *http.Request) map[string]string { return map[string]string{"id": chi.URLParam(r, "id")} }}
	return router.Collect("profiles", router.Route{Method: http.MethodPatch, Path: "/profiles/{id}", Operation: operationhttp.Metadata{Action: "profile.patch", ResourceType: "profile"}}.Bind(binding.JSONWithPath(patch, opts)))
}
func httpError(err error) error {
	if !errors.Is(err, profile.ErrDependency) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return err
	}
	status, code, message := 500, 50001, "internal server error"
	switch {
	case errors.Is(err, profile.ErrDependency):
		status, code, message = 502, 50201, "permission service unavailable"
	case errors.Is(err, profile.ErrInvalid):
		status, code, message = 400, 40001, "invalid profile change"
	case errors.Is(err, profile.ErrUnauthenticated):
		status, code, message = 401, 40101, "authentication required"
	case errors.Is(err, profile.ErrForbidden):
		status, code, message = 403, 40301, "profile access denied"
	case errors.Is(err, profile.ErrNotFound):
		status, code, message = 404, 40401, "profile not found"
	}
	return &httperror.Error{Status: status, Code: code, Message: message, Err: err}
}
