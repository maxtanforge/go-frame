package app_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/http/server"
	"github.com/stretchr/testify/require"
)

func TestTwoReplicasServeResidualTrafficDuringWithdrawal(t *testing.T) {
	type replica struct {
		url         string
		cancel      context.CancelFunc
		withdrawing chan struct{}
		done        chan error
	}
	start := func(delay time.Duration) replica {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		checker, err := health.New(health.Options{})
		require.NoError(t, err)
		r := router.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), router.Options{Health: checker})
		r.Get("/work", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		svc := listenerService{Server: server.New(":0", r), listener: ln}
		ctx, cancel := context.WithCancel(context.Background())
		state := replica{url: "http://" + ln.Addr().String(), cancel: cancel, withdrawing: make(chan struct{}), done: make(chan error, 1)}
		a, err := app.New(svc, app.Options{ShutdownTimeout: 5 * time.Second, ShutdownDelay: delay, OnShutdown: func() {
			checker.SetReady(false)
			close(state.withdrawing)
		}})
		require.NoError(t, err)
		t.Cleanup(func() { cancel(); require.NoError(t, svc.Close()) })
		go func() { state.done <- a.Run(ctx) }()
		return state
	}
	a, b := start(time.Second), start(0)
	client := &http.Client{Timeout: 2 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	status := func(url string, want int) {
		res, err := client.Get(url)
		require.NoError(t, err)
		_, copyErr := io.Copy(io.Discard, res.Body)
		closeErr := res.Body.Close()
		require.NoError(t, copyErr)
		require.NoError(t, closeErr)
		require.Equal(t, want, res.StatusCode)
	}
	status(a.url+"/readyz", 200)
	status(b.url+"/readyz", 200)
	a.cancel()
	select {
	case <-a.withdrawing:
	case <-time.After(2 * time.Second):
		t.Fatal("replica did not withdraw")
	}
	status(a.url+"/readyz", 503)
	status(a.url+"/work", 204)
	status(b.url+"/work", 204)
	select {
	case err := <-a.done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("replica did not stop")
	}
	status(b.url+"/readyz", 200)
	status(b.url+"/work", 204)
	b.cancel()
	select {
	case err := <-b.done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("second replica did not stop")
	}
}
