package record

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

type listStore struct {
	fakeStore
	calls  int
	window pagination.Window
	sorts  []pagination.Sort
	rows   []Record
	err    error
	ctx    context.Context
}

func (f *listStore) List(ctx context.Context, w pagination.Window, sorts []pagination.Sort) ([]Record, error) {
	f.calls++
	f.window = w
	f.sorts = sorts
	f.ctx = ctx
	return f.rows, f.err
}

func TestListHTTP(t *testing.T) {
	for _, raw := range []string{"", "current=2&size=1&sort=title,-id"} {
		f := &listStore{rows: []Record{{ID: 1, Title: "same"}, {ID: 2, Title: "same"}}}
		r := chi.NewRouter()
		Register(r, Service{IDs: testIDs, Repository: f}, testActor)
		req := httptest.NewRequest("GET", "/records?"+raw, nil)
		type key struct{}
		req = req.WithContext(context.WithValue(req.Context(), key{}, "marker"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
		require.Equal(t, 1, f.calls)
		require.Equal(t, "marker", f.ctx.Value(key{}))
		var body struct {
			Data pagination.Slice[json.RawMessage] `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.NotContains(t, w.Body.String(), `"total"`)
		require.NotContains(t, w.Body.String(), `"pages"`)
		if raw == "" {
			require.Equal(t, int64(1), f.window.Current())
			require.Equal(t, int64(20), f.window.Size())
			require.False(t, body.Data.HasMore)
		} else {
			require.Equal(t, int64(1), f.window.Offset())
			require.True(t, body.Data.HasMore)
			require.Len(t, body.Data.Records, 1)
			require.Equal(t, []pagination.Sort{{Field: "title"}, {Field: "id", Desc: true}}, f.sorts)
		}
	}
}
func TestListHTTPRejectsInvalidQuery(t *testing.T) {
	for _, raw := range []string{"current=", "size=", "sort=", "current=0", "current=-1", "current=%2B1", "size=101", "current=1000002&size=1", "current=9223372036854775808", "size=1.5", "current=1&current=2", "size=1&size=1", "sort=id&sort=title", "unknown=secret", "sort=title,title", "sort=-", "sort=title,", "sort=%2Bid", "sort=secret", "sort=id;secret", "sort=%zz", "current=%20"} {
		t.Run(raw[:min(len(raw), 40)], func(t *testing.T) {
			f := &listStore{}
			r := chi.NewRouter()
			Register(r, Service{IDs: testIDs, Repository: f}, testActor)
			req := httptest.NewRequest("GET", "/records", nil)
			req.URL.RawQuery = raw
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 400, w.Code)
			require.Zero(t, f.calls)
			require.NotContains(t, w.Body.String(), "secret")
		})
	}
	// 大小超限与全框架一致映射 413,不再按字段校验错误返回 400。
	f := &listStore{}
	r := chi.NewRouter()
	Register(r, Service{IDs: testIDs, Repository: f}, testActor)
	req := httptest.NewRequest("GET", "/records", nil)
	req.URL.RawQuery = "sort=" + strings.Repeat("x", 4092)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 413, w.Code)
	require.Zero(t, f.calls)
}
func TestListServicePolicyAndCancellation(t *testing.T) {
	f := &listStore{}
	s := Service{IDs: testIDs, Repository: f}
	for _, input := range []struct {
		current, size int64
		sorts         []pagination.Sort
	}{{0, 20, nil}, {1, 0, nil}, {1, 101, nil}, {1000002, 1, nil}, {1, 20, []pagination.Sort{{Field: "secret"}}}, {1, 20, []pagination.Sort{{Field: "id"}, {Field: "id"}}}} {
		_, err := s.List(context.Background(), input.current, input.size, input.sorts)
		require.ErrorIs(t, err, ErrInvalid)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.List(ctx, 1, 20, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, f.calls)
	f.err = errors.New("repository unavailable")
	_, err = s.List(context.Background(), 1, 20, nil)
	require.ErrorIs(t, err, f.err)
}

func TestListHTTPEmptyAndCanceled(t *testing.T) {
	f := &listStore{}
	r := chi.NewRouter()
	Register(r, Service{IDs: testIDs, Repository: f}, testActor)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/records", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"records":[]`)
	require.Contains(t, w.Body.String(), `"has_more":false`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/records", nil).WithContext(ctx))
	require.Equal(t, 1, f.calls)
}

func TestListQueryLengthBoundary(t *testing.T) {
	// Leading zeroes keep the integer valid while isolating the raw byte bound.
	valid := "current=" + strings.Repeat("0", 4087) + "1"
	_, _, _, err := parseListQuery(valid)
	require.NoError(t, err)
	_, _, _, err = parseListQuery(valid + "0")
	var oversized *httperror.Error
	require.ErrorAs(t, err, &oversized)
	require.Equal(t, 413, oversized.Status)
}
