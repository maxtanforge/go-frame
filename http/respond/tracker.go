package respond

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

type wroteTracker struct {
	http.ResponseWriter
	wrote bool
}

func (t *wroteTracker) WriteHeader(code int) {
	if t.wrote {
		return
	}
	t.ResponseWriter.WriteHeader(code)
	if code >= 200 || code == http.StatusSwitchingProtocols {
		t.wrote = true
	}
}
func (t *wroteTracker) Write(b []byte) (int, error) { t.wrote = true; return t.ResponseWriter.Write(b) }

// Flush implements http.Flusher, whose signature cannot report errors. Use
// http.NewResponseController(w).Flush() when the caller needs the flush error.
func (t *wroteTracker) Flush() { _ = t.FlushError() }

// FlushError preserves ResponseController's unsupported-operation error.
func (t *wroteTracker) FlushError() error {
	err := http.NewResponseController(t.ResponseWriter).Flush()
	if !errors.Is(err, http.ErrNotSupported) {
		t.wrote = true
	}
	return err
}
func (t *wroteTracker) Unwrap() http.ResponseWriter { return t.ResponseWriter }
func (t *wroteTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, r, err := http.NewResponseController(t.ResponseWriter).Hijack()
	if err == nil {
		t.wrote = true
	}
	return c, r, err
}
