package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type minimalWriter struct {
	header http.Header
	status int
}

func (w *minimalWriter) Header() http.Header         { return w.header }
func (w *minimalWriter) WriteHeader(n int)           { w.status = n }
func (w *minimalWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestAccessWriterUnsupportedOperationsDoNotCommit(t *testing.T) {
	w := &accessWriter{ResponseWriter: &minimalWriter{header: make(http.Header)}}
	require.ErrorIs(t, w.FlushError(), http.ErrNotSupported)
	_, _, err := w.Hijack()
	require.ErrorIs(t, err, http.ErrNotSupported)
	require.ErrorIs(t, w.Push("/asset", nil), http.ErrNotSupported)
	require.Zero(t, w.Status())
	require.False(t, w.Hijacked())
}

func TestAccessWriterBytesAndFinalStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &accessWriter{ResponseWriter: rec}
	n, err := w.ReadFrom(strings.NewReader("hello"))
	require.NoError(t, err)
	require.Equal(t, int64(5), n)
	require.Equal(t, n, w.BytesWritten())
	require.Equal(t, 200, w.Status())
	w.WriteHeader(500)
	require.Equal(t, 200, w.Status())
	require.Same(t, rec, w.Unwrap())
}

type readerFromWriter struct{ minimalWriter }

func (w *readerFromWriter) ReadFrom(r io.Reader) (int64, error) { return io.Copy(io.Discard, r) }

func TestAccessWriterReadFromFastPath(t *testing.T) {
	w := &accessWriter{ResponseWriter: &readerFromWriter{minimalWriter{header: make(http.Header)}}}
	n, err := w.ReadFrom(strings.NewReader("payload"))
	require.NoError(t, err)
	require.Equal(t, int64(7), n)
	require.Equal(t, n, w.BytesWritten())
	require.Equal(t, 200, w.Status())
}

type flushErrorWriter struct{ minimalWriter }

func (w *flushErrorWriter) FlushError() error { return errors.New("connection failed after headers") }

func TestAccessWriterFailedFlushCanCommit(t *testing.T) {
	w := &accessWriter{ResponseWriter: &flushErrorWriter{minimalWriter{header: make(http.Header)}}}
	require.Error(t, w.FlushError())
	require.Equal(t, 200, w.Status())
}
