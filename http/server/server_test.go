package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeAndShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServerOptionsValidation(t *testing.T) {
	for _, opts := range []Options{{ReadTimeout: -1}, {MaxHeaderBytes: -1}} {
		if _, err := NewWithOptions(":0", http.NotFoundHandler(), opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	s, err := NewWithOptions(":0", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.srv.ReadHeaderTimeout <= 0 || s.srv.IdleTimeout <= 0 || s.srv.MaxHeaderBytes <= 0 {
		t.Fatal("missing defaults")
	}
}

func TestWriteTimeoutDefaultsToBounded(t *testing.T) {
	s, err := NewWithOptions(":0", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.srv.WriteTimeout != 60*time.Second {
		t.Fatalf("default WriteTimeout = %v, want 60s (slow-client write bound)", s.srv.WriteTimeout)
	}
}

func TestNoWriteTimeoutSentinelAccepted(t *testing.T) {
	s, err := NewWithOptions(":0", nil, Options{WriteTimeout: NoWriteTimeout})
	if err != nil {
		t.Fatal(err)
	}
	if s.srv.WriteTimeout != 0 {
		t.Fatalf("NoWriteTimeout mapped to %v, want 0 (unlimited for streams)", s.srv.WriteTimeout)
	}
}

func TestOtherNegativeWriteTimeoutRejected(t *testing.T) {
	if _, err := NewWithOptions(":0", nil, Options{WriteTimeout: -2 * time.Second}); err == nil {
		t.Fatal("negative WriteTimeout other than NoWriteTimeout accepted")
	}
}
