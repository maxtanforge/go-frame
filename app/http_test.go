package app_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/http/server"
)

type listenerService struct {
	*server.Server
	listener net.Listener
}

func (s listenerService) Run() error { return s.Serve(s.listener) }

func TestHTTPShutdownWaitsForInflightRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	stopping := make(chan struct{})
	cleaned := make(chan struct{})
	svc := listenerService{Server: server.New(":0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})), listener: ln}
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Error(err)
		}
	})
	a, err := app.New(svc, app.Options{ShutdownTimeout: 2 * time.Second, OnShutdown: func() { close(stopping) }, Hooks: []app.Hook{{Name: "dependency", Stop: func(context.Context) error { close(cleaned); return nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	response := make(chan error, 1)
	go func() {
		client := http.Client{Timeout: 2 * time.Second}
		res, err := client.Get("http://" + ln.Addr().String())
		if err == nil {
			_, copyErr := io.Copy(io.Discard, res.Body)
			closeErr := res.Body.Close()
			if copyErr != nil {
				err = copyErr
			} else {
				err = closeErr
			}
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not enter")
	}
	cancel()
	select {
	case <-stopping:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not start")
	}
	select {
	case <-cleaned:
		t.Fatal("dependency closed before request drained")
	default:
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("dependency not closed")
	}
}
