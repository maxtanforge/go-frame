// Package responsewriter observes response commitment without buffering bodies.
package responsewriter

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
)

// Writer observes final commitment, including a body-less flush. It uses
// ResponseController for optional operations rather than assuming support.
type Writer struct {
	http.ResponseWriter
	status   int
	bytes    int64
	hijacked bool
}

func (w *Writer) Status() int                 { return w.status }
func (w *Writer) BytesWritten() int64         { return w.bytes }
func (w *Writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *Writer) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	if status >= 200 || status == 101 {
		w.status = status
	}
}
func (w *Writer) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}
func (w *Writer) Flush() {
	// http.Flusher has no error result; ResponseController users get FlushError.
	_ = w.FlushError()
}
func (w *Writer) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if w.status == 0 && !errors.Is(err, http.ErrNotSupported) {
		w.status = http.StatusOK
	}
	return err
}
func (w *Writer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, r, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return c, r, err
}
func (w *Writer) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}
func (w *Writer) ReadFrom(r io.Reader) (int64, error) {
	if reader, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		if w.status == 0 {
			w.WriteHeader(http.StatusOK)
		}
		n, err := reader.ReadFrom(r)
		w.bytes += n
		return n, err
	}
	return io.Copy(struct{ io.Writer }{w}, r)
}

func (w *Writer) Hijacked() bool { return w.hijacked }
