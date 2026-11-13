package binding_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/binding"
)

func TestTypedInterceptorsRunAfterValidationAndBeforeEncoding(t *testing.T) {
	type input struct {
		Name string `v:"required"`
	}
	type output struct{ Name string }
	var order []string
	wrap := func(name string) binding.Interceptor[input, output] {
		return func(next binding.Handler[input, output]) binding.Handler[input, output] {
			return func(ctx context.Context, in *input) (*output, error) {
				order = append(order, name+"+")
				out, err := next(ctx, in)
				order = append(order, name+"-")
				if out != nil {
					out.Name += ":" + name
				}
				return out, err
			}
		}
	}
	callback, err := binding.Intercept[input, output](func(_ context.Context, in *input) (*output, error) {
		order = append(order, "business")
		return &output{Name: in.Name}, nil
	}, wrap("one"), wrap("two"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := binding.JSON(callback)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"name":"Alice"}`, `{}`, `{"name":1}`} {
		order = nil
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if body == `{"name":"Alice"}` {
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Alice:two:one") || !reflect.DeepEqual(order, []string{"one+", "two+", "business", "two-", "one-"}) {
				t.Fatalf("%d %s %v", w.Code, w.Body, order)
			}
		} else if w.Code != 400 || len(order) != 0 {
			t.Fatalf("invalid input reached interceptor: %d %v", w.Code, order)
		}
	}
}

func TestInterceptorConstructionRejectsInvalidCallbacks(t *testing.T) {
	callback := func(context.Context, *int) (*int, error) { return nil, nil }
	if _, err := binding.Intercept[int, int](nil); err == nil {
		t.Fatal("nil accepted")
	}
	if _, err := binding.Intercept[int, int](callback, nil); err == nil {
		t.Fatal("nil interceptor accepted")
	}
	if _, err := binding.Intercept[int, int](callback, func(binding.Handler[int, int]) binding.Handler[int, int] { return nil }); err == nil {
		t.Fatal("nil wrapper accepted")
	}
	if _, err := binding.InterceptPair[int, int, int](nil); err == nil {
		t.Fatal("nil pair accepted")
	}
	if _, err := binding.InterceptPair[int, int, int](func(context.Context, *int, *int) (*int, error) { return nil, nil }, nil); err == nil {
		t.Fatal("nil pair wrapper accepted")
	}
}

func TestPairResultInterceptor(t *testing.T) {
	type path struct{ ID int }
	type body struct{ Name string }
	type output struct{ Name string }
	callback, err := binding.InterceptPair[path, body, output](func(_ context.Context, p *path, in *body) (*output, error) {
		if p.ID != 42 {
			t.Fatal("path lost")
		}
		return &output{Name: in.Name}, nil
	}, func(next binding.PairHandler[path, body, output]) binding.PairHandler[path, body, output] {
		return func(ctx context.Context, p *path, in *body) (*output, error) {
			out, err := next(ctx, p, in)
			if out != nil {
				out.Name += ":wrapped"
			}
			return out, err
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := binding.JSONWithPath(callback, binding.Options{PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "42"} }})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/42", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Alice:wrapped") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
