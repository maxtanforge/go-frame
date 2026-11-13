package respond_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/validation"
)

type fieldInput struct {
	Name string `json:"name"`
}

type privateInput struct{}

type deadlineInput struct{}

func (deadlineInput) Validate() error {
	return errors.Join(context.DeadlineExceeded, &validation.FieldError{Field: "name", Rule: validation.RuleRequired})
}

func TestJSONValidatorChildDeadlineUsesSafeInputError(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return request.DecodeJSON[deadlineInput](w, r)
	}).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"data":null`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func (privateInput) Validate() error {
	return &httperror.Error{Status: 500, Err: &validation.FieldError{Field: "private", Rule: validation.RuleRequired}}
}

func TestJSONValidatorDoesNotPublishAppErrorCause(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return request.DecodeJSON[privateInput](w, r)
	}).ServeHTTP(w, r)
	if w.Code != 400 || strings.Contains(w.Body.String(), "private") || !strings.Contains(w.Body.String(), `"data":null`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func (fieldInput) Validate() error {
	return fmt.Errorf("private cause: %w", &validation.FieldError{Field: "name", Rule: validation.RuleRequired})
}

func TestSharedValidationEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		rule        validation.Rule
		run         respond.Handler
	}{
		{"query", "size", validation.RuleFormat, func(http.ResponseWriter, *http.Request) (any, error) {
			return request.DecodeQuery("size=private", struct {
				Size int `query:"size"`
			}{})
		}},
		{"path", "id", validation.RuleRequired, func(http.ResponseWriter, *http.Request) (any, error) {
			return request.DecodePath[struct {
				ID int `path:"id"`
			}](nil)
		}},
		{"json", "name", validation.RuleRequired, func(w http.ResponseWriter, r *http.Request) (any, error) { return request.DecodeJSON[fieldInput](w, r) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			respond.Adapter(tc.run).ServeHTTP(w, r)
			var got struct {
				Success bool
				Data    any
				Message string
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || got.Success || got.Data != nil || got.Message == "" || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}

func TestPrivateErrorsAndSchemaStayPrivate(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{request.ErrSchema, 500},
		{errors.New("private secret"), 500},
		{&httperror.Error{Status: 409, Message: "Conflict", Err: &validation.FieldError{Field: "private", Rule: validation.RuleRequired}}, 409},
	} {
		w := httptest.NewRecorder()
		respond.WriteError(w, httptest.NewRequest("GET", "/", nil), tc.err)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private") || !strings.Contains(w.Body.String(), `"data":null`) {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
}
