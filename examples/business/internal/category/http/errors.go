package categoryhttp

import (
	"context"
	"errors"

	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/http/httperror"
)

func httpError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	status, code, message := 500, 50001, "internal server error"
	switch {
	case errors.Is(err, category.ErrInvalid):
		status, code, message = 400, 40001, "invalid category input"
	case errors.Is(err, category.ErrUnauthenticated):
		status, code, message = 401, 40101, "authentication required"
	case errors.Is(err, category.ErrForbidden):
		status, code, message = 403, 40301, "category access denied"
	case errors.Is(err, category.ErrNotFound):
		status, code, message = 404, 40401, "category not found"
	case errors.Is(err, category.ErrImmutable):
		status, code, message = 409, 40901, "category cannot be modified"
	}
	return &httperror.Error{Status: status, Code: code, Message: message, Err: err}
}
