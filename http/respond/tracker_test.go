package respond

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
)

func TestDirectWrite(t *testing.T) {
	t.Run("csv_bypasses_envelope", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Adapter(func(w http.ResponseWriter, _ *http.Request) (any, error) {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("a,b\n1,2\n"))
			return nil, nil
		}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/export", nil))
		assert.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Equal(t, "a,b\n1,2\n", rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "code")
	})

	t.Run("written_then_error_no_rewrite", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Adapter(func(w http.ResponseWriter, _ *http.Request) (any, error) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			return nil, errors.New("late failure")
		}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, "partial", rec.Body.String())
	})

	t.Run("written_then_panic_no_append", func(t *testing.T) {
		rec := httptest.NewRecorder()
		assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
			Adapter(func(w http.ResponseWriter, _ *http.Request) (any, error) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("partial"))
				panic("boom")
			}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		})
		assert.Equal(t, "partial", rec.Body.String())
	})

	t.Run("header_only_still_envelopes", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Adapter(func(w http.ResponseWriter, _ *http.Request) (any, error) {
			w.Header().Set("X-Custom", "1") // 只设头未写体 → 仍走包络
			return map[string]int{"n": 1}, nil
		}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "\"code\":200")
		assert.Equal(t, "1", rec.Header().Get("X-Custom"))
	})
}

// Review Focus #4:Timeout 包在 tracker 外层,超时路径不得产出混杂体。
func TestTrackerWithTimeout(t *testing.T) {
	rec := httptest.NewRecorder()
	h := chimw.Timeout(30 * time.Millisecond)(Adapter(func(_ http.ResponseWriter, r *http.Request) (any, error) {
		<-r.Context().Done() // 模拟慢查询直到超时
		return nil, r.Context().Err()
	}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	// 无论 chi 超时路径还是 Adapter 错误路径,响应必须是完整单一 JSON 或空体,不得混杂
	if rec.Body.Len() > 0 {
		assert.True(t, json.Valid(rec.Body.Bytes()), "body must be valid JSON, got: %s", rec.Body.String())
	}
}
