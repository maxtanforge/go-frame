package request_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/validation"
)

type query struct {
	Current int64  `query:"current"`
	Size    uint8  `query:"size"`
	Active  bool   `query:"active"`
	Search  string `query:"search"`
}

func TestQueryDefaultsAndTypes(t *testing.T) {
	defaults := query{Current: 1, Size: 20, Active: true}
	got, err := request.DecodeQuery("current=2&size=10&active=false&search=a%2Bb", defaults)
	if err != nil || got != (query{2, 10, false, "a+b"}) {
		t.Fatalf("got %#v: %v", got, err)
	}
	got, err = request.DecodeQuery("", defaults)
	if err != nil || got != defaults {
		t.Fatalf("defaults: %#v %v", got, err)
	}
}

func TestQueryRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	for _, raw := range []string{"current=", "current=1&current=2", "secret=private", "current=9223372036854775808", "size=256", "size=-1", "active=1", "current=%2B1", "current=1.1", "search=%zz", "search=x;y"} {
		_, err := request.DecodeQuery(raw, query{})
		if !errors.Is(err, validation.ErrInvalid) {
			t.Errorf("%q: %v", raw[:min(40, len(raw))], err)
			continue
		}
		if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
			t.Fatal("raw input disclosed", err)
		}
	}
	_, err := request.DecodeQuery("size=256", query{})
	var field *validation.FieldError
	if !errors.As(err, &field) || field.Field != "size" || field.Rule != "format" {
		t.Fatalf("field error: %v", err)
	}
}

type validated struct {
	ID int64 `path:"id"`
}

func (v *validated) Validate() error {
	if v.ID < 1 {
		return errors.New("private validation detail")
	}
	return nil
}

func TestPathRequiredAndValidation(t *testing.T) {
	got, err := request.DecodePath[validated](map[string]string{"id": "9007199254740993"})
	if err != nil || got.ID != 9007199254740993 {
		t.Fatalf("snowflake precision: %#v %v", got, err)
	}
	for _, values := range []map[string]string{nil, {"id": ""}, {"id": "0"}, {"id": "1", "secret": "private"}} {
		_, err = request.DecodePath[validated](values)
		if !errors.Is(err, validation.ErrInvalid) || strings.Contains(err.Error(), "private") {
			t.Fatalf("invalid path: %v", err)
		}
	}
}

// 用户输入的大小超限与 Prepared 路径一致映射 413;声明错误仍是 ErrSchema(500)。
func TestSizeOverrunMapsTo413(t *testing.T) {
	cases := []struct {
		name string
		call func() error
	}{
		{"query-raw", func() error {
			_, err := request.DecodeQuery("search="+strings.Repeat("x", 4090), query{})
			return err
		}},
		{"path-combined", func() error {
			params := map[string]string{}
			for i := 0; i < 8; i++ {
				params["k"+string(rune('a'+i))] = strings.Repeat("v", 512)
			}
			_, err := request.DecodePath[validated](params)
			return err
		}},
		{"path-member", func() error {
			_, err := request.DecodePath[validated](map[string]string{"id": strings.Repeat("1", 5000)})
			return err
		}},
		{"path-count", func() error {
			params := map[string]string{}
			for i := 0; i < 65; i++ {
				params["p"+string(rune('a'+i%26))+string(rune('a'+i/26))] = "1"
			}
			_, err := request.DecodePath[validated](params)
			return err
		}},
	}
	for _, tc := range cases {
		var h *httperror.Error
		err := tc.call()
		if !errors.As(err, &h) || h.Status != 413 {
			t.Errorf("%s: expected 413 httperror, got %v", tc.name, err)
		}
	}
}

// DecodeJSON 只接受至多一个 DecodeOptions,与 multipart/binding 一致拒绝误传。
func TestDecodeJSONRejectsExtraOptions(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":1}`))
	req.Header.Set("Content-Type", "application/json")
	_, err := request.DecodeJSON[validated](httptest.NewRecorder(), req,
		request.DecodeOptions{MaxBytes: 1 << 20}, request.DecodeOptions{MaxBytes: 2 << 20})
	if !errors.Is(err, request.ErrSchema) {
		t.Fatalf("expected ErrSchema, got %v", err)
	}
}

func TestSchemaErrorsAreNotClientErrors(t *testing.T) {
	for _, check := range []func() error{
		func() error { _, e := request.DecodeQuery("", 0); return e },
		func() error {
			_, e := request.DecodeQuery("", struct {
				A []string `query:"a"`
			}{})
			return e
		},
		func() error {
			_, e := request.DecodeQuery("", struct {
				A string `query:"a"`
				B string `query:"a"`
			}{})
			return e
		},
		func() error { _, e := request.DecodeQuery("", struct{ A string }{}); return e },
	} {
		err := check()
		if !errors.Is(err, request.ErrSchema) || errors.Is(err, validation.ErrInvalid) {
			t.Fatalf("schema: %v", err)
		}
	}
}

type fieldQuery struct {
	Name string `query:"name"`
}

func (q fieldQuery) Validate() error {
	return &validation.FieldError{Field: q.Name, Rule: validation.RuleRequired}
}

func TestValidatorOnlyPublishesDeclaredFields(t *testing.T) {
	for _, tc := range []struct{ raw, field string }{{"name=name", "name"}, {"name=private", ""}} {
		_, err := request.DecodeQuery(tc.raw, fieldQuery{})
		var field *validation.FieldError
		if !errors.As(err, &field) || field.Field != tc.field || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe diagnostic: %v", err)
		}
	}
}
