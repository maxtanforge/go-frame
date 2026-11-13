package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chain mirrors trace then access logging.
func chain(base *slog.Logger, next http.Handler) http.Handler {
	return trace.Middleware()(AccessLog(base, AccessOptions{})(next))
}

func hitLogger() (*strings.Builder, http.Handler) {
	buf := &strings.Builder{}
	lg := slog.New(slog.NewTextHandler(buf, nil))
	h := chain(lg, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		logger.FromContext(r.Context()).Info("hit")
	}))
	return buf, h
}

func TestRequestLogHonorsUpstreamTraceID(t *testing.T) {
	buf, h := hitLogger()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(trace.HeaderTraceID, "test-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Contains(t, buf.String(), "hit")
	assert.Contains(t, buf.String(), "trace_id=test-123", "上游 x-trace-id 必须透传为日志键")
	assert.Equal(t, "test-123", rec.Header().Get(trace.HeaderTraceID), "必须回写响应头")
	assert.NotContains(t, buf.String(), "request_id=")
}

func TestRequestLogGeneratesDistinctIDs(t *testing.T) {
	ids := make(chan string, 2)
	buf := &strings.Builder{}
	lg := slog.New(slog.NewTextHandler(buf, nil))
	h := chain(lg, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ids <- trace.FromContext(r.Context())
		logger.FromContext(r.Context()).Info("hit")
	}))

	for i := 0; i < 2; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}

	first, second := <-ids, <-ids
	require.NotEmpty(t, first, "缺省必须自动生成非空 ID")
	require.NotEmpty(t, second)
	assert.NotEqual(t, first, second, "两次请求 ID 必须不同")
	assert.Contains(t, buf.String(), "trace_id=")
}

func TestRequestLogUsesTraceID(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	h := AccessLog(base, AccessOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		logger.FromContext(r.Context()).Info("inside")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(trace.IntoContext(req.Context(), "tid-99"))
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Contains(t, buf.String(), `"trace_id":"tid-99"`)
	assert.NotContains(t, buf.String(), "request_id")
}

func TestRecovererCatchPanic(t *testing.T) {
	h := respond.Recoverer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
