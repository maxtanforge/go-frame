package respond_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

func TestSchemaErrorsPrecedeAutomaticValidationMapping(t *testing.T) {
	field := &validation.FieldError{Field: "name", Rule: validation.RuleRequired}
	collection := validation.NewErrors(validation.Issue{Path: validation.Path{"name"}, Code: "required", Message: "公开输入提示"})
	poisoned := validation.NewErrors(validation.Issue{Code: "invalid code"})
	for _, schema := range []error{validation.ErrSchema, request.ErrSchema, poisoned} {
		for _, input := range []error{field, collection} {
			for _, joined := range []error{errors.Join(schema, input), errors.Join(input, schema)} {
				t.Run(fmt.Sprintf("%T/%T/%s", schema, input, joined), func(t *testing.T) {
					var logs strings.Builder
					ctx := logger.IntoContext(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)))
					w := httptest.NewRecorder()
					r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
					respond.Adapter(func(http.ResponseWriter, *http.Request) (any, error) {
						return nil, fmt.Errorf("private declaration detail: %w", joined)
					}).ServeHTTP(w, r)
					require.Equal(t, http.StatusInternalServerError, w.Code)
					require.NotContains(t, w.Body.String(), "private declaration detail")
					require.NotContains(t, w.Body.String(), "公开输入提示")
					require.Contains(t, w.Body.String(), `"data":null`)
					require.Contains(t, logs.String(), "private declaration detail")
					require.Equal(t, 1, strings.Count(logs.String(), "level=ERROR"))
				})
			}
		}
	}
}

func TestSchemaMappingRetainsExplicitAndContextPrecedence(t *testing.T) {
	field := &validation.FieldError{Field: "name", Rule: validation.RuleRequired}
	schema := errors.Join(validation.ErrSchema, field)
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"field", field, 400},
		{"deadline", errors.Join(context.DeadlineExceeded, schema), 500},
		{"public", &httperror.Error{Status: 409, Message: "Conflict", Err: schema}, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			respond.WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), tc.err)
			require.Equal(t, tc.status, w.Code)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	respond.WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx), schema)
	require.Empty(t, w.Body.String())
}

func TestSchemaRequestCategoryPreservesDiagnostic(t *testing.T) {
	require.EqualError(t, request.ErrSchema, "request: invalid DTO schema")
	require.ErrorIs(t, fmt.Errorf("wrapped: %w", request.ErrSchema), request.ErrSchema)
	require.ErrorIs(t, request.ErrSchema, validation.ErrSchema)
}
