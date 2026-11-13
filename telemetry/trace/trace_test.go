package trace

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/maxtanforge/go-frame/id"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware(t *testing.T) {
	makeHandler := func(captured *string) http.Handler {
		return Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*captured = FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))
	}

	t.Run("upstream_passthrough", func(t *testing.T) {
		var got string
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(HeaderTraceID, "abc-123")
		rec := httptest.NewRecorder()
		makeHandler(&got).ServeHTTP(rec, req)
		assert.Equal(t, "abc-123", got)
		assert.Equal(t, "abc-123", rec.Header().Get(HeaderTraceID))
	})

	t.Run("empty_header_generates", func(t *testing.T) {
		var got string
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(HeaderTraceID, "")
		rec := httptest.NewRecorder()
		makeHandler(&got).ServeHTTP(rec, req)
		assert.NotEmpty(t, got) // 空串头不透传,走生成
		assert.Equal(t, got, rec.Header().Get(HeaderTraceID))
	})

	t.Run("generated_is_snowflake_identifier", func(t *testing.T) {
		var got string
		rec := httptest.NewRecorder()
		makeHandler(&got).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		n, err := strconv.ParseInt(got, 10, 64)
		require.NoError(t, err)
		require.Positive(t, n)
		require.Zero(t, (n>>12)&1023)
	})

	t.Run("distinct_ids", func(t *testing.T) {
		var a, b string
		for _, out := range []*string{&a, &b} {
			makeHandler(out).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		}
		assert.NotEqual(t, a, b)
	})

	t.Run("with_snowflake_injected", func(t *testing.T) {
		sf, err := id.NewSnowflake(7)
		require.NoError(t, err)
		var got string
		h := Middleware(WithSnowflake(sf))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = FromContext(r.Context())
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		n, err := strconv.ParseInt(got, 10, 64)
		require.NoError(t, err)
		assert.EqualValues(t, 7, (n>>12)&1023) // node 位可解码
	})
}

func TestInvalidIncomingIDIsReplaced(t *testing.T) {
	for _, id := range []string{"has spaces", "has\ttab", string(make([]byte, 129))} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set(HeaderTraceID, id)
		w := httptest.NewRecorder()
		Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			require.NotEqual(t, id, FromContext(r.Context()))
			require.Regexp(t, `^[0-9]+$`, FromContext(r.Context()))
		})).ServeHTTP(w, r)
	}
}

type failingGenerator struct{ err error }

func (f failingGenerator) Next() (int64, error) { return 0, f.err }

func TestGeneratorFailureRejectsAndReports(t *testing.T) {
	sentinel := errors.New("clock backwards")
	var seen error
	h := Middleware(
		WithGenerator(failingGenerator{err: sentinel}),
		WithGenerateError(func(err error) { seen = err }),
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler ran without an ID") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, sentinel, seen, "失败必须上报给观察者")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Empty(t, rec.Header().Get(HeaderTraceID))
	require.NotContains(t, rec.Body.String(), sentinel.Error())
}

func TestGeneratorFailureWithoutObserver(t *testing.T) {
	h := Middleware(WithGenerator(failingGenerator{err: errors.New("x")}))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil)) })
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestNilGeneratorsFallBack(t *testing.T) {
	var sf *id.Snowflake
	for name, option := range map[string]Option{
		"nil snowflake":       WithSnowflake(nil),
		"typed nil generator": WithGenerator(sf),
		"nil interface":       WithGenerator(nil),
	} {
		t.Run(name, func(t *testing.T) {
			var got string
			h := Middleware(option, WithGenerateError(func(error) { t.Error("absent generator reported failure") }))(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = FromContext(r.Context()); w.WriteHeader(200) }))
			w := httptest.NewRecorder()
			require.NotPanics(t, func() { h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) })
			require.Equal(t, 200, w.Code)
			require.Regexp(t, `^[0-9]+$`, got)
			require.True(t, ValidID(got))
			require.Equal(t, got, w.Header().Get(HeaderTraceID))
		})
	}
}

func TestDefaultSnowflakeSharedAcrossMiddleware(t *testing.T) {
	const count = 512
	values := make(chan string, count)
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				values <- FromContext(r.Context())
			})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		}()
	}
	workers.Wait()
	close(values)
	seen := make(map[string]bool, count)
	for value := range values {
		require.False(t, seen[value], "duplicate across middleware instances")
		seen[value] = true
	}
	require.Len(t, seen, count)
}
