package respond

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/stretchr/testify/require"
)

type brokenWriter struct{ plainWriter }

func (w *brokenWriter) Write([]byte) (int, error) { return 0, errors.New("socket write detail") }

type failedFlusher struct{ plainWriter }

func (w *failedFlusher) FlushError() error { w.WriteHeader(200); return errors.New("flush detail") }

type hijackWriter struct {
	plainWriter
	conn net.Conn
}

func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.conn, nil, nil }
func TestResponseFailuresLogged(t *testing.T) {
	for _, tc := range []struct {
		data   any
		writer http.ResponseWriter
		detail string
	}{{make(chan int), httptest.NewRecorder(), "unsupported type"}, {"ok", &brokenWriter{plainWriter{header: make(http.Header)}}, "socket write detail"}} {
		log := &strings.Builder{}
		req := httptest.NewRequest("GET", "/", nil).WithContext(ctxLogger(log))
		Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return tc.data, nil })(tc.writer, req)
		require.Contains(t, log.String(), tc.detail)
	}
}
func TestFlushFailureMayCommit(t *testing.T) {
	w := &wroteTracker{ResponseWriter: &failedFlusher{plainWriter{header: make(http.Header)}}}
	require.Error(t, w.FlushError())
	require.True(t, w.wrote)
}
func TestHijackCommit(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	w := &wroteTracker{ResponseWriter: &hijackWriter{plainWriter: plainWriter{header: make(http.Header)}, conn: a}}
	got, _, err := http.NewResponseController(w).Hijack()
	require.NoError(t, err)
	require.Equal(t, a, got)
	require.True(t, w.wrote)
	w = &wroteTracker{ResponseWriter: &plainWriter{header: make(http.Header)}}
	_, _, err = w.Hijack()
	require.ErrorIs(t, err, http.ErrNotSupported)
	require.False(t, w.wrote)
}
func TestDecodeNullPointer(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader("null"))
	r.Header.Set("Content-Type", "application/json")
	_, err := request.DecodeJSON[*validatedInput](httptest.NewRecorder(), r)
	require.Error(t, err)
}
func TestDecodeOversizeTrailingWhitespace(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"x"}`+strings.Repeat(" ", 100)))
	r.Header.Set("Content-Type", "application/json")
	_, err := request.DecodeJSON[validatedInput](httptest.NewRecorder(), r, request.DecodeOptions{MaxBytes: 32})
	var app *httperror.Error
	require.ErrorAs(t, err, &app)
	require.Equal(t, 413, app.Status)
}
func BenchmarkDecodeJSON(b *testing.B) {
	for b.Loop() {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"x"}`))
		r.Header.Set("Content-Type", "application/json")
		if _, err := request.DecodeJSON[validatedInput](httptest.NewRecorder(), r); err != nil {
			b.Fatal(err)
		}
	}
}
func TestDecodeWithoutValidator(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"n":1}`))
	r.Header.Set("Content-Type", "application/json")
	value, err := request.DecodeJSON[map[string]int](httptest.NewRecorder(), r)
	require.NoError(t, err)
	require.Equal(t, 1, value["n"])
}
