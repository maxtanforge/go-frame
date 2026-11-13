package router_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/trace"
	"github.com/stretchr/testify/require"
)

type failedTraceGenerator struct{}

func (failedTraceGenerator) Next() (int64, error) { return 0, errors.New("generator failed") }

func TestTraceObserverPanicUsesErrorEnvelope(t *testing.T) {
	var logs bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&logs, nil))
	calls := 0
	r := router.New(lg, router.Options{Trace: []trace.Option{
		trace.WithGenerator(failedTraceGenerator{}),
		trace.WithGenerateError(func(error) { calls++; panic("private observer cause") }),
	}})
	reached := false
	r.Get("/test", func(w http.ResponseWriter, _ *http.Request) { reached = true; w.WriteHeader(200) })
	w := httptest.NewRecorder()
	require.NotPanics(t, func() { r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil)) })
	require.Equal(t, 1, calls)
	require.False(t, reached)
	require.Equal(t, 500, w.Code)
	var envelope respond.Envelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, 500, envelope.Code)
	require.False(t, envelope.Success)
	require.NotContains(t, w.Body.String(), "private observer cause")
	require.Equal(t, 1, strings.Count(logs.String(), `"msg":"panic recovered"`))
	require.Contains(t, logs.String(), "private observer cause")
	require.Contains(t, logs.String(), `"stack":`)
}

// Both recovery layers must propagate protocol aborts without appending JSON.
func TestOuterRecoveryPreservesCommittedAbort(t *testing.T) {
	var logs bytes.Buffer
	r := router.New(slog.New(slog.NewJSONHandler(&logs, nil)), router.Options{})
	r.Get("/stream", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("private failure after commitment")
	})
	w := httptest.NewRecorder()
	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		r.ServeHTTP(w, httptest.NewRequest("GET", "/stream", nil))
	})
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "partial", w.Body.String())
	require.Equal(t, 1, strings.Count(logs.String(), `"msg":"panic recovered"`))
	require.Equal(t, 1, strings.Count(logs.String(), `"msg":"request completed"`))
}

type panickingTraceGenerator struct{ payload any }

func (g panickingTraceGenerator) Next() (int64, error) { panic(g.payload) }

func TestTraceGeneratorPanicUsesConfiguredLogger(t *testing.T) {
	var logs bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&logs, nil))
	r := router.New(l, router.Options{Trace: []trace.Option{trace.WithGenerator(panickingTraceGenerator{"generator panic"})}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 1, strings.Count(logs.String(), `"msg":"panic recovered"`))
	require.Contains(t, logs.String(), "generator panic")
	require.NotContains(t, w.Body.String(), "generator panic")

	logs.Reset()
	r = router.New(l, router.Options{Trace: []trace.Option{trace.WithGenerator(panickingTraceGenerator{http.ErrAbortHandler})}})
	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	})
	require.Empty(t, logs.String(), "protocol abort must not be logged as an application panic")
}
