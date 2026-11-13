package healthhttp

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/health"
	"github.com/stretchr/testify/require"
)

func TestOptionalFailureDegradesWithoutRejectingTraffic(t *testing.T) {
	for _, optional := range []bool{false, true} {
		checker, err := health.New(health.Options{Checks: []health.Check{{Name: "cache", Optional: optional, Probe: func(context.Context) error { return errors.New("secret detail") }}}})
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		New(checker).ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
		if optional {
			require.Equal(t, 200, rec.Code)
			require.Contains(t, rec.Body.String(), `"status":"degraded"`)
			require.Contains(t, rec.Body.String(), `"cache":"unavailable"`)
		} else {
			require.Equal(t, 503, rec.Code)
		}
		require.NotContains(t, rec.Body.String(), "secret detail")
		checker.SetReady(false)
		rec = httptest.NewRecorder()
		New(checker).ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
		require.Equal(t, 503, rec.Code)
	}
}

func TestStuckOptionalCannotStarveRequiredChecks(t *testing.T) {
	var requiredCalls, optionalCalls atomic.Int32
	release, finished := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(release)
		if optionalCalls.Load() > 0 {
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Error("optional probe did not finish")
			}
		}
	})
	checker, err := health.New(health.Options{
		Timeout: 30 * time.Millisecond, Concurrency: 1, OptionalConcurrency: 1,
		Checks: []health.Check{
			{Name: "optional", Optional: true, Probe: func(context.Context) error {
				optionalCalls.Add(1)
				<-release
				close(finished)
				return nil
			}},
			{Name: "required", Probe: func(context.Context) error { requiredCalls.Add(1); return nil }},
		},
	})
	require.NoError(t, err)
	for range 2 {
		rec := httptest.NewRecorder()
		New(checker).ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
		require.Equal(t, 200, rec.Code)
		require.Contains(t, rec.Body.String(), `"status":"degraded"`)
	}
	require.Equal(t, int32(2), requiredCalls.Load())
	require.Equal(t, int32(1), optionalCalls.Load())
}

func TestOptionalPoolValidation(t *testing.T) {
	_, err := health.New(health.Options{OptionalConcurrency: -1})
	require.Error(t, err)
}

func TestHealthyOptionalAndRequiredFailure(t *testing.T) {
	for _, failRequired := range []bool{false, true} {
		checker, err := health.New(health.Options{Checks: []health.Check{
			{Name: "cache", Optional: true, Probe: func(context.Context) error { return nil }},
			{Name: "database", Probe: func(context.Context) error {
				if failRequired {
					return errors.New("database unavailable")
				}
				return nil
			}},
		}})
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		New(checker).ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
		if failRequired {
			require.Equal(t, 503, rec.Code)
		} else {
			require.Equal(t, 200, rec.Code)
			require.Contains(t, rec.Body.String(), `"status":"ready"`)
			require.Contains(t, rec.Body.String(), `"cache":"ok"`)
		}
	}
}

func TestDrainDuringOptionalProbeWins(t *testing.T) {
	var checker *health.Checker
	var err error
	checker, err = health.New(health.Options{Checks: []health.Check{{Name: "cache", Optional: true, Probe: func(context.Context) error {
		checker.SetReady(false)
		return errors.New("unavailable")
	}}}})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	New(checker).ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	require.Equal(t, 503, rec.Code)
	require.Contains(t, rec.Body.String(), "service is draining")
}
