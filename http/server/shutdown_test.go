package server

import (
	"context"
	"net/http"
	"testing"
)

func TestShutdownIdleServer(t *testing.T) {
	s := New("127.0.0.1:0", http.NotFoundHandler())
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
