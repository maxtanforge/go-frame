package healthhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/health"
)

func TestCheckerDeadlineAndSafeFailure(t *testing.T) {
	c, err := health.New(health.Options{Timeout: 20 * time.Millisecond, Checks: []health.Check{{Name: "db", Probe: func(ctx context.Context) error { <-ctx.Done(); return errors.New("password=secret") }}}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	New(c).ServeHTTP(r, httptest.NewRequest("GET", "/readyz", nil))
	if r.Code != 503 || strings.Contains(r.Body.String(), "secret") {
		t.Fatalf("response=%d %s", r.Code, r.Body.String())
	}
}

func TestCheckerDrainAndLiveness(t *testing.T) {
	c, err := health.New(health.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/readyz", "/healthz"} {
		r := httptest.NewRecorder()
		New(c).ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 {
			t.Fatalf("%s=%d", path, r.Code)
		}
	}
	c.SetReady(false)
	r := httptest.NewRecorder()
	New(c).ServeHTTP(r, httptest.NewRequest("GET", "/readyz", nil))
	if r.Code != 503 {
		t.Fatalf("draining=%d", r.Code)
	}
	r = httptest.NewRecorder()
	New(c).ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
	if r.Code != 200 {
		t.Fatal("liveness follows readiness")
	}
}

func TestCheckerBoundedAcrossRequests(t *testing.T) {
	var running, peak atomic.Int32
	probe := func(ctx context.Context) error {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	c, err := health.New(health.Options{Timeout: 10 * time.Millisecond, Concurrency: 2, Checks: []health.Check{{Name: "a", Probe: probe}, {Name: "b", Probe: probe}, {Name: "c", Probe: probe}}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() {
			r := httptest.NewRecorder()
			New(c).ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if peak.Load() > 2 {
		t.Fatalf("concurrency=%d", peak.Load())
	}
}

func TestCheckerRejectsInvalidChecks(t *testing.T) {
	for _, opts := range []health.Options{{Timeout: -1}, {Concurrency: -1}, {Checks: []health.Check{{Name: "x"}}}, {Checks: []health.Check{{Name: "x", Probe: func(context.Context) error { return nil }}, {Name: "x", Probe: func(context.Context) error { return nil }}}}} {
		if _, err := health.New(opts); err == nil {
			t.Fatalf("accepted %+v", opts)
		}
	}
}
