package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/concurrency"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/stretchr/testify/require"
)

func TestAdmissionRejectsOverloadAndWaitTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		waiters int
		err     error
	}{
		{"full", 0, concurrency.ErrOverloaded},
		{"wait_timeout", 1, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := concurrency.NewBoundedLimiter(1, tc.waiters)
			require.NoError(t, err)
			release, err := l.Acquire(context.Background())
			require.NoError(t, err)
			defer release()
			var observed AdmissionEvent
			calls := 0
			wrap, err := Admission(l, AdmissionOptions{WaitTimeout: 20 * time.Millisecond,
				OnAdmission: func(_ context.Context, event AdmissionEvent) { observed = event; calls++ }})
			require.NoError(t, err)
			called := false
			h := wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			h.ServeHTTP(w, r)
			require.False(t, called)
			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			require.Contains(t, w.Body.String(), `"code":503`)
			require.NotContains(t, w.Body.String(), tc.err.Error())
			require.Equal(t, 1, calls)
			require.ErrorIs(t, observed.Err, tc.err)
			require.GreaterOrEqual(t, observed.Duration, time.Duration(0))
			if tc.waiters > 0 {
				require.Positive(t, observed.Duration)
			}
			require.NoError(t, r.Context().Err())
			require.Zero(t, l.Stats().Waiting)
		})
	}
}

func TestAdmissionPreservesRequestContext(t *testing.T) {
	l, err := concurrency.NewBoundedLimiter(1, 1)
	require.NoError(t, err)
	ctx := context.Background()
	wrap, err := Admission(l, AdmissionOptions{WaitTimeout: 10 * time.Millisecond})
	require.NoError(t, err)
	h := wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, ctx, r.Context())
		time.Sleep(25 * time.Millisecond)
		require.NoError(t, r.Context().Err())
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Zero(t, l.InUse())
}

func TestAdmissionParentCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		l, err := concurrency.NewBoundedLimiter(1, 1)
		require.NoError(t, err)
		release, err := l.Acquire(context.Background())
		require.NoError(t, err)
		wrap, err := Admission(l, AdmissionOptions{WaitTimeout: time.Second})
		require.NoError(t, err)
		h := wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("canceled request admitted") }))
		var ctx context.Context
		var cancel context.CancelFunc
		if deadline {
			ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
		} else {
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
		cancel()
		release()
		if deadline {
			require.Equal(t, http.StatusGatewayTimeout, w.Code)
		} else {
			require.Empty(t, w.Body.String())
		}
		require.Zero(t, l.Stats().Waiting)
		require.Zero(t, l.InUse())
	}
}

func TestAdmissionReleaseAfterPanicAndObserverIsolation(t *testing.T) {
	l, err := concurrency.NewBoundedLimiter(1, 0)
	require.NoError(t, err)
	var logs strings.Builder
	ctx := logger.IntoContext(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)))
	wrap, err := Admission(l, AdmissionOptions{OnAdmission: func(context.Context, AdmissionEvent) { panic("observer") }})
	require.NoError(t, err)
	h := wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("handler") }))
	require.PanicsWithValue(t, "handler", func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	})
	require.Zero(t, l.InUse())
	require.Contains(t, logs.String(), "admission observer panic")
	called := false
	h = wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	require.True(t, called)
	require.Zero(t, l.InUse())
}

func TestAdmissionValidation(t *testing.T) {
	legacy, err := concurrency.NewLimiter(1)
	require.NoError(t, err)
	bounded, err := concurrency.NewBoundedLimiter(1, 1)
	require.NoError(t, err)
	for _, tc := range []struct {
		l    *concurrency.Limiter
		opts AdmissionOptions
	}{
		{nil, AdmissionOptions{}},
		{legacy, AdmissionOptions{WaitTimeout: time.Second}},
		{bounded, AdmissionOptions{}},
		{bounded, AdmissionOptions{WaitTimeout: -time.Second}},
	} {
		wrap, err := Admission(tc.l, tc.opts)
		require.Nil(t, wrap)
		require.Error(t, err)
	}
}
