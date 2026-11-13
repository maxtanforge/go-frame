package metric

import (
	"context"
	"math"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecorderEnforcesTotalSeriesLimit(t *testing.T) {
	r, err := NewRequestRecorder(Options{})
	require.NoError(t, err)
	for i := 0; i < 600; i++ {
		if err := r.Observe(context.Background(), Observation{Route: "/routes/" + strconv.Itoa(i), Method: "GET", DurationMS: 1}); err != nil {
			t.Error(err)
			return
		}
	}
	require.LessOrEqual(t, len(r.Snapshot()), MaxKeys)
}

func TestConcurrentSnapshotsPreserveHistogramInvariants(t *testing.T) {
	h, err := NewHistogram([]float64{1, 2, 3})
	require.NoError(t, err)
	var writers sync.WaitGroup
	writers.Add(1)
	start := make(chan struct{})
	go func() {
		defer writers.Done()
		<-start
		for i := 0; i < 100_000; i++ {
			if err := h.Observe(0); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	close(start)
	defer writers.Wait()
	for i := 0; i < 10_000; i++ {
		s := h.Snapshot()
		var previous uint64
		for _, bucket := range s.Buckets {
			if bucket.Count < previous || bucket.Count > s.Count {
				t.Fatalf("invalid concurrent snapshot: count=%d buckets=%v", s.Count, s.Buckets)
			}
			previous = bucket.Count
		}
	}
}

func TestInvalidObservationDoesNotPoisonHistogram(t *testing.T) {
	h, err := NewHistogram([]float64{1})
	require.NoError(t, err)
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		require.Error(t, h.Observe(value))
	}
	snapshot := h.Snapshot()
	require.Zero(t, snapshot.Count)
	require.Zero(t, snapshot.Sum)
}

func TestRecorderNormalizesBeforeExportAndPreservesContext(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "identity"))
	cancel()
	var recorder *RequestRecorder
	var seen Observation
	recorder, err := NewRequestRecorder(Options{OnObserve: func(got context.Context, o Observation) {
		require.Same(t, ctx, got)
		seen = o
		require.Contains(t, recorder.Snapshot(), RequestKey{Route: o.Route, Method: o.Method})
	}})
	require.NoError(t, err)
	require.NoError(t, recorder.Observe(ctx, Observation{Route: "/bad\nroute", Method: "invented", DurationMS: 5}))
	require.Equal(t, Observation{Route: "_OTHER", Method: "_OTHER", DurationMS: 5}, seen)
}

func TestRecorderRejectsInvalidInputBeforeAllocatingKeys(t *testing.T) {
	called := false
	recorder, err := NewRequestRecorder(Options{OnObserve: func(context.Context, Observation) { called = true }})
	require.NoError(t, err)
	//nolint:staticcheck // Verify nil context contract.
	require.Error(t, recorder.Observe(nil, Observation{DurationMS: 1}))
	for _, duration := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		require.Error(t, recorder.Observe(context.Background(), Observation{Route: "/bad", Method: "GET", DurationMS: duration}))
	}
	require.Empty(t, recorder.Snapshot())
	require.False(t, called)
}

func TestHistogramCopiesBoundsAndRejectsOverflowAtomically(t *testing.T) {
	bounds := []float64{1}
	histogram, err := NewHistogram(bounds)
	require.NoError(t, err)
	bounds[0] = 99
	require.Equal(t, float64(1), histogram.Snapshot().Buckets[0].UpperBound)
	require.NoError(t, histogram.Observe(math.MaxFloat64))
	before := histogram.Snapshot()
	require.Error(t, histogram.Observe(math.MaxFloat64))
	require.Equal(t, before, histogram.Snapshot())
	defaults := DefaultBuckets()
	defaults[0] = 999
	require.Equal(t, float64(1), DefaultBuckets()[0])
}

func TestSeriesLimitAcrossAllMethods(t *testing.T) {
	recorder, err := NewRequestRecorder(Options{})
	require.NoError(t, err)
	methods := []string{"GET", "POST", "PUT", "DELETE", "HEAD", "PATCH", "OPTIONS", "CONNECT", "TRACE", "invalid"}
	for i := 0; i < 600; i++ {
		for _, method := range methods {
			require.NoError(t, recorder.Observe(context.Background(), Observation{Route: "/routes/" + strconv.Itoa(i), Method: method, DurationMS: 1}))
		}
	}
	require.Len(t, recorder.Snapshot(), MaxKeys)
	var count uint64
	for _, h := range recorder.Snapshot() {
		count += h.Count
	}
	require.Equal(t, uint64(6000), count)
}
