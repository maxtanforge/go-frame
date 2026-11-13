package binding_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

type input struct {
	Current int64 `d:"1" v:"gte=1" label:"页码"`
	Size    int64 `d:"20" v:"gte=1,lte=100" label:"页大小"`
}

type privateRuleInput struct{ Mode string }

func (in *privateRuleInput) Validate(c *validation.Context) {
	inner := validation.NewErrors(validation.Issue{Code: "validation", Message: "private diagnostic"})
	var err error = &httperror.Error{Status: 500, Message: "server error", Err: inner}
	if in.Mode == "deadline" || in.Mode == "deadline_check" {
		err = errors.Join(context.DeadlineExceeded, inner)
	}
	if in.Mode == "canceled" {
		err = errors.Join(context.Canceled, inner)
	}
	if in.Mode == "schema" {
		err = errors.Join(validation.ErrSchema, context.DeadlineExceeded, inner)
	}
	if in.Mode == "limit" {
		err = errors.Join(validation.ErrLimit, context.DeadlineExceeded, inner)
	}
	if in.Mode == "application" {
		err = &httperror.Error{Status: 503, Message: "try again", Err: errors.Join(context.DeadlineExceeded, inner)}
	}
	if in.Mode == "check" || in.Mode == "deadline_check" {
		validation.Check(c, "Mode", in.Mode, func(string) error { return err })
		return
	}
	c.Merge(err)
}

func TestCustomContextErrorRetainsExplicitCategories(t *testing.T) {
	h, err := binding.Query(func(context.Context, *privateRuleInput) (*privateRuleInput, error) {
		t.Fatal("handler reached")
		return nil, nil
	})
	require.NoError(t, err)
	for _, tc := range []struct {
		mode   string
		status int
	}{{"schema", 500}, {"limit", 400}, {"application", 503}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/?mode="+tc.mode, nil))
		require.Equal(t, tc.status, w.Code)
		require.NotContains(t, w.Body.String(), "private diagnostic")
	}
}

func TestXMLCustomChildDeadlineDiagnosticStaysPrivate(t *testing.T) {
	h, err := binding.XML(func(context.Context, *privateRuleInput) (*privateRuleInput, error) {
		t.Fatal("handler reached")
		return nil, nil
	})
	require.NoError(t, err)
	r := httptest.NewRequest("POST", "/", strings.NewReader(`<privateRuleInput><Mode>deadline</Mode></privateRuleInput>`))
	r.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.NotContains(t, w.Body.String(), "private diagnostic")
	require.Equal(t, 500, w.Code)
}
func TestCustomInternalCausesAndDeadlinesStayPrivate(t *testing.T) {
	h, err := binding.Query(func(_ context.Context, in *privateRuleInput) (*privateRuleInput, error) {
		t.Fatal("handler reached")
		return in, nil
	})
	require.NoError(t, err)
	for _, mode := range []string{"merge", "check", "deadline", "deadline_check", "canceled"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/?mode="+mode, nil))
		require.NotContains(t, w.Body.String(), "private diagnostic")
		require.Equal(t, 500, w.Code)
	}
}

func TestTypedQueryDefaultAndReject(t *testing.T) {
	called := 0
	h, err := binding.Query(func(_ context.Context, in *input) (*input, error) { called++; return in, nil })
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"Current":1`)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?current=0&size=abc", nil))
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "页码不能小于 1")
	require.Equal(t, 1, called)
}
func TestJSONDuplicateKeysAndNullable(t *testing.T) {
	type body struct {
		Name string `v:"required"`
	}
	h, err := binding.JSON(func(_ context.Context, in *body) (*body, error) { return in, nil })
	require.NoError(t, err)
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"a","name":"b"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
}

type pathInput struct {
	ID int64 `v:"gte=1" label:"ID"`
}

func TestPathBindingBindsAndRejects(t *testing.T) {
	params := map[string]string{"id": "42"}
	h, err := binding.Path(func(_ context.Context, in *pathInput) (*pathInput, error) { return in, nil },
		binding.Options{PathParams: func(*http.Request) map[string]string { return params }})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/items/42", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"ID":42`)
	params["id"] = "0"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/items/0", nil))
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "ID不能小于 1")
	delete(params, "id")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/items/", nil))
	require.Equal(t, 400, w.Code)
}

type patchBody struct {
	Name string `v:"required" label:"名称"`
}

// 路径与请求体问题合并为一次诊断;两个来源都失败时路径问题排在前面。
func TestJSONWithPathCombinesPathAndBodyIssues(t *testing.T) {
	params := map[string]string{"id": "0"}
	h, err := binding.JSONWithPath(func(_ context.Context, _ *pathInput, b *patchBody) (*patchBody, error) {
		return b, nil
	}, binding.Options{PathParams: func(*http.Request) map[string]string { return params }})
	require.NoError(t, err)
	r := httptest.NewRequest("PATCH", "/items/0", strings.NewReader(`{"name":""}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "ID不能小于 1")

	params["id"] = "7"
	r = httptest.NewRequest("PATCH", "/items/7", strings.NewReader(`{"name":""}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "名称")

	r = httptest.NewRequest("PATCH", "/items/7", strings.NewReader(`{"name":"frame"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"Name":"frame"`)
}

func TestBindingRejectsMisconfiguration(t *testing.T) {
	handler := func(_ context.Context, in *input) (*input, error) { return in, nil }
	_, err := binding.Query(handler, binding.Options{}, binding.Options{})
	require.ErrorIs(t, err, validation.ErrSchema)
	_, err = binding.Path(handler)
	require.ErrorIs(t, err, validation.ErrSchema, "Path 绑定必须提供 PathParams")
	_, err = binding.Query[input, input](nil)
	require.ErrorIs(t, err, validation.ErrSchema)
}
