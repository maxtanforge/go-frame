package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouterHealthzSmoke(t *testing.T) {
	h := New(logger.New(logger.Options{Level: "info"}), Options{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	// 缺 checker 是配置错误：复用 healthhttp 的 fail-closed 语义，不谎报健康。
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.EqualValues(t, 503, body["code"])
}

func TestRouterReadyzSmoke(t *testing.T) {
	h := New(logger.New(logger.Options{Level: "info"}), Options{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestRouterUnknownPath404(t *testing.T) {
	h := New(logger.New(logger.Options{Level: "info"}), Options{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRouterTraceHeaderAndEnvelope(t *testing.T) {
	h := New(logger.New(logger.Options{Level: "info"}), Options{})
	h.Get("/ping", respond.Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return map[string]string{"pong": "true"}, nil
	}))
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.NotEmpty(t, rec.Header().Get("x-trace-id")) // Trace 中间件回写
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	assert.EqualValues(t, 200, m["code"])
	assert.Equal(t, "成功", m["message"])
	assert.Equal(t, rec.Header().Get("x-trace-id"), m["traceId"]) // 头与包络同源
}
