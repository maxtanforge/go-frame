package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

func TestCanceledOperationIsNotSuccessfulRequest(t *testing.T) {
	var event middleware.RequestEvent
	router := router.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), router.Options{
		OnRequest: func(_ context.Context, e middleware.RequestEvent) { event = e },
	})
	router.Get("/", respond.Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return nil, context.Canceled }))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 500, rec.Code)
	require.Contains(t, rec.Body.String(), `"success":false`)
	require.Equal(t, 500, event.Status)
	require.False(t, event.Canceled)
}

func TestCommittedPanicAbortsTransport(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, adapted := range []bool{false, true} {
			name := "http1/native"
			if http2 {
				name = "http2/native"
			}
			if adapted {
				name += "/adapted"
			}
			t.Run(name, func(t *testing.T) {
				events := make(chan middleware.RequestEvent, 1)
				release := make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				router := router.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), router.Options{
					OnRequest: func(_ context.Context, e middleware.RequestEvent) { events <- e },
				})
				handler := func(w http.ResponseWriter, r *http.Request) {
					if _, err := io.WriteString(w, "partial"); err != nil {
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					panic("stream failed")
				}
				if adapted {
					router.Get("/", respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) { handler(w, r); return nil, nil }))
				} else {
					router.Get("/", handler)
				}
				// httptest listens only on loopback; no wildcard firewall permission.
				srv := httptest.NewUnstartedServer(router)
				srv.EnableHTTP2 = http2
				srv.StartTLS()
				t.Cleanup(func() { unblock(); srv.Close() })
				client := srv.Client()
				client.Timeout = 3 * time.Second
				response, err := client.Get(srv.URL)
				require.NoError(t, err)
				defer func() { _ = response.Body.Close() }()
				wantProto := 1
				if http2 {
					wantProto = 2
				}
				require.Equal(t, wantProto, response.ProtoMajor)
				buf := make([]byte, len("partial"))
				_, err = io.ReadFull(response.Body, buf)
				require.NoError(t, err)
				require.Equal(t, "partial", string(buf))
				unblock()
				rest, err := io.ReadAll(response.Body)
				require.Error(t, err, "partial stream must not end successfully")
				require.Empty(t, rest, "no JSON error envelope after commitment")
				select {
				case event := <-events:
					require.Equal(t, 200, event.Status)
					require.True(t, event.Aborted)
				case <-time.After(3 * time.Second):
					t.Fatal("missing completion event")
				}
			})
		}
	}
}
