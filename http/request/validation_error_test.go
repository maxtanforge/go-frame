package request_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

type diagnosticInput struct {
	ID string `query:"id" path:"id"`
}

func (v diagnosticInput) Validate() error {
	field := &validation.FieldError{Field: "id", Rule: validation.RuleRequired}
	if v.ID == "timeout" {
		return errors.Join(context.DeadlineExceeded, field)
	}
	return &httperror.Error{Status: 500, Message: "private message", Err: field}
}

func TestQueryPathValidationErrorBoundary(t *testing.T) {
	for _, value := range []string{"timeout", "private"} {
		for _, source := range []string{"query", "path"} {
			t.Run(source+"/"+value, func(t *testing.T) {
				var err error
				if source == "query" {
					_, err = request.DecodeQuery("id="+value, diagnosticInput{})
				} else {
					_, err = request.DecodePath[diagnosticInput](map[string]string{"id": value})
				}
				w := httptest.NewRecorder()
				respond.WriteError(w, httptest.NewRequest("GET", "/", nil), err)
				status := 400
				if value == "timeout" {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				}
				require.Equal(t, status, w.Code)
				require.NotContains(t, w.Body.String(), "fields")
				require.NotContains(t, w.Body.String(), "private message")
			})
		}
	}
}
