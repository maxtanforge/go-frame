package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/concurrency"
	"github.com/stretchr/testify/require"
)

func TestAdmissionSustainedHTTPOverload(t *testing.T) {
	l, err := concurrency.NewBoundedLimiter(3, 2)
	require.NoError(t, err)
	var peak atomic.Int32
	var mu sync.Mutex
	var durations []time.Duration
	wrap, err := Admission(l, AdmissionOptions{WaitTimeout: 50 * time.Millisecond, OnAdmission: func(_ context.Context, event AdmissionEvent) {
		mu.Lock()
		durations = append(durations, event.Duration)
		mu.Unlock()
		s := l.Stats()
		if s.InUse > 3 || s.Waiting > 2 {
			t.Error("admission limits exceeded")
		}
	}})
	require.NoError(t, err)
	srv := httptest.NewServer(wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int32(l.InUse())
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-time.After(20 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	})))
	defer srv.Close()
	transport := &http.Transport{MaxConnsPerHost: 24, MaxIdleConnsPerHost: 24}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	var admitted, rejected atomic.Int32
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			for range 20 {
				response, err := client.Get(srv.URL)
				if err != nil {
					t.Error("HTTP load request failed")
					return
				}
				_, copyErr := io.Copy(io.Discard, response.Body)
				closeErr := response.Body.Close()
				if copyErr != nil || closeErr != nil {
					t.Error("HTTP response read failed")
					return
				}
				switch response.StatusCode {
				case 204:
					admitted.Add(1)
				case 503:
					rejected.Add(1)
				default:
					t.Errorf("unexpected status %d", response.StatusCode)
				}
			}
		})
	}
	wg.Wait()
	require.Positive(t, admitted.Load())
	require.Positive(t, rejected.Load())
	require.EqualValues(t, 480, admitted.Load()+rejected.Load())
	require.LessOrEqual(t, peak.Load(), int32(3))
	require.Zero(t, l.Stats().InUse)
	require.Zero(t, l.Stats().Waiting)
	// Stop load, then verify the same service admits a fresh request.
	response, err := client.Get(srv.URL)
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	mu.Lock()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p99 := durations[(len(durations)-1)*99/100]
	mu.Unlock()
	t.Logf("loopback requests=480 admitted=%d rejected=%d peak_active=%d admission_p99=%s", admitted.Load(), rejected.Load(), peak.Load(), p99)
}
