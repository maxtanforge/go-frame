package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestAbortIsNotObservedAsSuccess(t *testing.T) {
	var event RequestEvent
	h := AccessLog(nil, AccessOptions{OnRequest: func(_ context.Context, e RequestEvent) { event = e }})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	require.PanicsWithValue(t, http.ErrAbortHandler, func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) })
	require.Zero(t, event.Status)
	require.True(t, event.Aborted)
}

func TestQuietHealthStillObservesMetrics(t *testing.T) {
	var logs bytes.Buffer
	observed := 0
	h := AccessLog(slog.New(slog.NewJSONHandler(&logs, nil)), AccessOptions{QuietHealth: true, OnRequest: func(context.Context, RequestEvent) { observed++ }})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))
	require.Empty(t, logs.String())
	require.Equal(t, 1, observed)
}

func TestObserverPanicDoesNotRewriteResponse(t *testing.T) {
	var logs bytes.Buffer
	h := AccessLog(slog.New(slog.NewJSONHandler(&logs, nil)), AccessOptions{OnRequest: func(context.Context, RequestEvent) { panic("metrics broken") }})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	w := httptest.NewRecorder()
	require.NotPanics(t, func() { h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) })
	require.Equal(t, 204, w.Code)
	require.Empty(t, w.Body.String())
	require.Contains(t, logs.String(), "request observer panic")
}

func TestCanceledRequestHasNoInventedStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var event RequestEvent
	h := AccessLog(nil, AccessOptions{OnRequest: func(_ context.Context, e RequestEvent) { event = e }})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	require.True(t, event.Canceled)
	require.Zero(t, event.Status)
}

func TestFlushedStreamRetainsStatusOnCancellationOrAbort(t *testing.T) {
	for _, abort := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var event RequestEvent
		h := AccessLog(nil, AccessOptions{OnRequest: func(_ context.Context, e RequestEvent) { event = e }})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			require.NoError(t, http.NewResponseController(w).Flush())
			cancel()
			if abort {
				panic(http.ErrAbortHandler)
			}
		}))
		serve := func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/events", nil).WithContext(ctx))
		}
		if abort {
			require.PanicsWithValue(t, http.ErrAbortHandler, serve)
		} else {
			serve()
		}
		require.Equal(t, 200, event.Status)
		cancel()
	}
}

func TestQuietHealthMatchesMountedPrefix(t *testing.T) {
	var logs bytes.Buffer
	sub := chi.NewRouter()
	sub.Use(AccessLog(slog.New(slog.NewJSONHandler(&logs, nil)), AccessOptions{QuietHealth: true}))
	sub.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	root := chi.NewRouter()
	root.Mount("/api", sub)
	rec := httptest.NewRecorder()
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, logs.String(), "request completed", "挂载前缀下的健康探测不应产生访问日志")
}
