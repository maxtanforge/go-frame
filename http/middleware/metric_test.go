package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetricMethodHasBoundedLabels(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "CONNECT", "OPTIONS", "TRACE", "PATCH"} {
		require.Equal(t, method, MetricMethod(method))
	}
	for _, method := range []string{"", "get", "CUSTOM-001", "CUSTOM-002", "PROPFIND"} {
		require.Equal(t, "_OTHER", MetricMethod(method))
	}
}

func TestEventSeparatesRawAndMetricMethod(t *testing.T) {
	var events []RequestEvent
	h := AccessLog(slog.New(slog.NewJSONHandler(io.Discard, nil)), AccessOptions{
		OnRequest: func(_ context.Context, e RequestEvent) { events = append(events, e) },
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(405) }))
	for _, method := range []string{"CUSTOM-001", "CUSTOM-002"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/", nil))
	}
	require.Len(t, events, 2)
	require.NotEqual(t, events[0].Method, events[1].Method)
	for _, e := range events {
		require.Equal(t, "_OTHER", e.MetricMethod)
		require.Equal(t, "<unmatched>", e.Route)
	}
}
