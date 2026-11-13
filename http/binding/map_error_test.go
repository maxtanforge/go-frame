package binding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

type mappedInput struct {
	ID int `v:"gte=1"`
}

type mappedConstructor struct {
	name    string
	build   func(func(context.Context) error, Options) (http.Handler, error)
	request func() *http.Request
}

func mappedConstructors() []mappedConstructor {
	single := func(f func(context.Context) error) func(context.Context, *mappedInput) (*mappedInput, error) {
		return func(ctx context.Context, in *mappedInput) (*mappedInput, error) { return in, f(ctx) }
	}
	pair := func(f func(context.Context) error) func(context.Context, *mappedInput, *mappedInput) (*mappedInput, error) {
		return func(ctx context.Context, _ *mappedInput, in *mappedInput) (*mappedInput, error) { return in, f(ctx) }
	}
	queryRequest := func() *http.Request { return httptest.NewRequest("GET", "/?id=7", nil) }
	bodyRequest := func() *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"id":7}`))
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	return []mappedConstructor{
		{"query", func(f func(context.Context) error, o Options) (http.Handler, error) { return Query(single(f), o) }, queryRequest},
		{"json", func(f func(context.Context) error, o Options) (http.Handler, error) { return JSON(single(f), o) }, bodyRequest},
		{"path", func(f func(context.Context) error, o Options) (http.Handler, error) { return Path(single(f), o) }, queryRequest},
		{"json_with_path", func(f func(context.Context) error, o Options) (http.Handler, error) { return JSONWithPath(pair(f), o) }, bodyRequest},
		{"path_with_query", func(f func(context.Context) error, o Options) (http.Handler, error) { return PathWithQuery(pair(f), o) }, queryRequest},
		{"form", func(f func(context.Context) error, o Options) (http.Handler, error) { return Form(single(f), o) }, func() *http.Request {
			r := httptest.NewRequest("POST", "/", strings.NewReader("id=7"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			return r
		}},
	}
}

func TestCallbackErrorMappingAcrossBindings(t *testing.T) {
	original := errors.New("private business failure")
	for _, constructor := range mappedConstructors() {
		t.Run(constructor.name, func(t *testing.T) {
			calls := 0
			o := Options{
				PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "7"} },
				MapError: func(err error) error {
					calls++
					require.ErrorIs(t, err, original)
					return &httperror.Error{Status: 409, Code: 4901, Message: "business conflict"}
				},
			}
			h, err := constructor.build(func(context.Context) error { return original }, o)
			require.NoError(t, err)
			// Changing the caller's options cannot replace the captured mapper.
			o.MapError = func(error) error { t.Fatal("replacement mapper ran"); return nil }
			w := httptest.NewRecorder()
			h.ServeHTTP(w, constructor.request())
			require.Equal(t, 1, calls)
			require.Equal(t, 409, w.Code)
			require.Contains(t, w.Body.String(), `"code":4901`)
			require.Contains(t, w.Body.String(), "business conflict")
			require.NotContains(t, w.Body.String(), original.Error())
		})
	}
}

func TestCallbackMappingBypassesSuccessAndCancellation(t *testing.T) {
	original := errors.New("business failure")
	for _, constructor := range mappedConstructors() {
		for _, tc := range []struct {
			name        string
			callbackErr error
			cancel      bool
			status      int
		}{
			{"success", nil, false, 200},
			{"operation_cancel", errors.Join(original, context.Canceled), false, 500},
			{"operation_deadline", fmt.Errorf("operation: %w", context.DeadlineExceeded), false, 500},
			{"request_cancel_during_callback", original, true, 200},
		} {
			t.Run(constructor.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				h, err := constructor.build(func(context.Context) error {
					if tc.cancel {
						cancel()
					}
					return tc.callbackErr
				}, Options{
					PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "7"} },
					MapError:   func(error) error { t.Fatal("mapper must be bypassed"); return nil },
				})
				require.NoError(t, err)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, constructor.request().WithContext(ctx))
				require.Equal(t, tc.status, w.Code)
				if tc.cancel {
					require.Empty(t, w.Body.String())
				}
			})
		}
	}
}

func TestCallbackMappingBypassesInputAndConstructionErrors(t *testing.T) {
	for _, constructor := range mappedConstructors() {
		t.Run(constructor.name, func(t *testing.T) {
			o := Options{
				PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "0"} },
				MapError:   func(error) error { t.Fatal("mapper must not handle input errors"); return nil },
			}
			h, err := constructor.build(func(context.Context) error { t.Fatal("callback reached"); return nil }, o)
			require.NoError(t, err)
			r := constructor.request()
			if constructor.name == "query" {
				r = httptest.NewRequest("GET", "/?id=abc", nil)
			}
			if constructor.name == "json" {
				r = httptest.NewRequest("POST", "/", strings.NewReader(`{"id":`))
				r.Header.Set("Content-Type", "application/json")
			}
			if constructor.name == "form" {
				r = httptest.NewRequest("POST", "/", strings.NewReader("id=0"))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, 400, w.Code)
			_, err = Query[struct{ Value chan int }, mappedInput](func(context.Context, *struct{ Value chan int }) (*mappedInput, error) { return nil, nil }, o)
			require.ErrorIs(t, err, validation.ErrSchema)
		})
	}
}

func TestMapCallbackErrorRetainsCausesAndIdentity(t *testing.T) {
	original := &httperror.Error{Status: 500, Err: errors.New("storage failure")}
	mapped := &httperror.Error{Status: 409, Message: "conflict"}
	wrapped := fmt.Errorf("mapped: %w", original)
	for _, tc := range []struct {
		name   string
		mapper func(error) error
		want   error
	}{
		{"absent", nil, original},
		{"nil_result", func(error) error { return nil }, original},
		{"already_wrapped", func(error) error { return wrapped }, wrapped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mapCallbackError(context.Background(), original, tc.mapper)
			require.Same(t, tc.want, got)
			require.ErrorIs(t, got, original)
		})
	}
	calls := 0
	got := mapCallbackError(context.Background(), original, func(error) error { calls++; return mapped })
	require.Equal(t, 1, calls)
	require.ErrorIs(t, got, original)
	require.ErrorIs(t, got, original.Err)
	var app *httperror.Error
	require.ErrorAs(t, got, &app)
	require.Same(t, mapped, app, "mapped error has precedence over the original HTTP error")
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := errors.Join(original, cause)
		require.Same(t, err, mapCallbackError(context.Background(), err, func(error) error { t.Fatal("context error mapped"); return nil }))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Same(t, original, mapCallbackError(ctx, original, func(error) error { t.Fatal("canceled request mapped"); return nil }))
	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	require.Same(t, original, mapCallbackError(deadlineCtx, original, func(error) error { t.Fatal("expired request mapped"); return nil }))
}

func TestCallbackNilMappingResultKeepsFailureAcrossBindings(t *testing.T) {
	for _, constructor := range mappedConstructors() {
		t.Run(constructor.name, func(t *testing.T) {
			calls := 0
			h, err := constructor.build(func(context.Context) error { return errors.New("private failure") }, Options{
				PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "7"} },
				MapError:   func(error) error { calls++; return nil },
			})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, constructor.request())
			require.Equal(t, 1, calls)
			require.Equal(t, 500, w.Code)
			require.NotContains(t, w.Body.String(), "private failure")
		})
	}
}

type unmappedValidatorInput struct{ ID int }

func (*unmappedValidatorInput) Validate(c *validation.Context) {
	c.Merge(&httperror.Error{Status: 403, Message: "validator rejected input"})
}

func TestCallbackMappingBypassesCustomValidatorAndPairedBodyErrors(t *testing.T) {
	o := Options{
		PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "7"} },
		MapError:   func(error) error { t.Fatal("input error mapped"); return nil },
	}
	h, err := Query(func(context.Context, *unmappedValidatorInput) (*mappedInput, error) {
		t.Fatal("callback reached after custom validation error")
		return nil, nil
	}, o)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?id=7", nil))
	require.Equal(t, 403, w.Code)
	for _, constructor := range mappedConstructors() {
		if constructor.name != "json_with_path" && constructor.name != "path_with_query" {
			continue
		}
		t.Run(constructor.name, func(t *testing.T) {
			h, err := constructor.build(func(context.Context) error { t.Fatal("callback reached after paired input error"); return nil }, o)
			require.NoError(t, err)
			for _, value := range []string{"abc", "0"} {
				r := httptest.NewRequest("GET", "/?id="+value, nil)
				if constructor.name == "json_with_path" {
					body := `{"id":0}`
					if value == "abc" {
						body = `{"id":`
					}
					r = httptest.NewRequest("POST", "/", strings.NewReader(body))
					r.Header.Set("Content-Type", "application/json")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				require.Equal(t, 400, w.Code)
			}
		})
	}
}

func TestCallbackMappingDoesNotRetryMappedDeadline(t *testing.T) {
	calls := 0
	original := errors.New("business deadline")
	got := mapCallbackError(context.Background(), original, func(error) error { calls++; return context.DeadlineExceeded })
	require.Equal(t, 1, calls)
	require.ErrorIs(t, got, original)
	require.ErrorIs(t, got, context.DeadlineExceeded)
}

func TestCallbackMappingBypassesResponseSerializationErrors(t *testing.T) {
	h, err := Query(func(context.Context, *mappedInput) (*chan int, error) {
		ch := make(chan int)
		return &ch, nil
	}, Options{MapError: func(error) error { t.Fatal("serialization failure mapped"); return nil }})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?id=7", nil))
	require.Equal(t, 500, w.Code)
}

func TestCustomContextFailureKeepsPrivateCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		private := validation.NewErrors(validation.Issue{Code: "validation", Message: "private diagnostic"})
		original := errors.Join(cause, private)
		got := customValidationError(original)
		require.ErrorIs(t, got, cause)
		require.ErrorIs(t, got, private)
		var app *httperror.Error
		require.ErrorAs(t, got, &app)
		require.Equal(t, 500, app.Status)
		require.Same(t, original, app.Err)
		require.NotContains(t, app.Message, "private diagnostic")
	}
}
