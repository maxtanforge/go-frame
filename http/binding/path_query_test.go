package binding_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

type countedQuery struct {
	Size  int `d:"20" v:"gte=1" label:"query-size"`
	Calls int `q:"-"`
}

func (q *countedQuery) Validate(*validation.Context) { q.Calls++ }
func TestPathWithQueryIndependentValidationAndFreshValues(t *testing.T) {
	params := map[string]string{"id": "0"}
	calls := 0
	h, err := binding.PathWithQuery(func(_ context.Context, p *pathInput, q *countedQuery) (*countedQuery, error) {
		calls++
		require.EqualValues(t, 7, p.ID)
		require.Equal(t, 1, q.Calls)
		return q, nil
	}, binding.Options{PathParams: func(*http.Request) map[string]string { return params }})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?size=0", nil))
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "ID不能小于 1")

	require.Zero(t, calls)
	delete(params, "id")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 400, w.Code)
	params["id"] = "7"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?size=0", nil))
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "query-size")
	require.Zero(t, calls)
	for _, url := range []string{"/?size=2", "/"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		require.Equal(t, 200, w.Code)
	}
	require.Contains(t, w.Body.String(), `"Size":20`)
	require.Equal(t, 2, calls)
}
func TestPathWithQueryConstructionAndHardErrors(t *testing.T) {
	handler := func(context.Context, *pathInput, *input) (*input, error) {
		t.Fatal("callback reached")
		return nil, nil
	}
	_, err := binding.PathWithQuery(handler)
	require.ErrorIs(t, err, validation.ErrSchema)
	_, err = binding.PathWithQuery[pathInput, input, input](nil)
	require.ErrorIs(t, err, validation.ErrSchema)
	opts := binding.Options{PathParams: func(*http.Request) map[string]string { return map[string]string{"id": "7"} }}
	type invalid struct{ Value chan int }
	_, err = binding.PathWithQuery(func(context.Context, *pathInput, *invalid) (*input, error) { return nil, nil }, opts)
	require.ErrorIs(t, err, validation.ErrSchema)
	h, err := binding.PathWithQuery(handler, opts)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	require.Empty(t, w.Body.String())
	opts.Limits = request.Options{MaxBytes: 10}
	h, err = binding.PathWithQuery(handler, opts)
	require.NoError(t, err)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?current=1&size=2", nil))
	require.Equal(t, 413, w.Code)
}
