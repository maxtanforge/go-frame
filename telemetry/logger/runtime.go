package logger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RuntimeOptions configures owned logging. Without File, stdout is always enabled.
type RuntimeOptions struct {
	Options
	File   *FileOptions
	Stdout bool
	// OnError runs synchronously outside locks, at most once per second.
	// Return promptly and use a concurrency-safe independent output channel.
	OnError func(error)
}

// Runtime owns file handles and serializes writes and archive publication.
// A bounded worker performs compression and retention; a full queue applies
// backpressure to rotation. Close drains maintenance before returning.
// Close does not close stdout. Writes are not fsync-durable.
type Runtime struct {
	mu         sync.Mutex
	logger     *slog.Logger
	level      slog.LevelVar
	sinks      []io.Writer
	file       *fileWriter
	closed     bool
	closeDone  chan struct{}
	closeErr   error
	errors     atomic.Uint64
	reporting  atomic.Bool
	lastReport time.Time
	onError    func(error)
}

func strictLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("invalid log level %q", s)
}

// Open validates configuration and opens the file before returning.
func Open(o RuntimeOptions) (*Runtime, error) {
	level, e := strictLevel(o.Level)
	if e != nil {
		return nil, e
	}
	if e = validateFormat(o.Format); e != nil {
		return nil, e
	}
	if _, e = logLocation(o.Timezone); e != nil {
		return nil, e
	}
	r := &Runtime{onError: o.OnError, closeDone: make(chan struct{})}
	r.level.Set(level)
	if o.File != nil {
		r.file, e = openFile(*o.File)
		if e != nil {
			return nil, e
		}
		r.file.startMaintenance(r.countError)
		r.sinks = append(r.sinks, r.file)
	}
	if o.File == nil || o.Stdout {
		r.sinks = append(r.sinks, consoleOutput(os.Stdout, o.Format))
	}
	h := outputHandler(o.Options, runtimeWriter{r}, &r.level)
	if o.StackOnError {
		h = stackHandler{Handler: h}
	}
	r.logger = slog.New(reportHandler{Handler: h, r: r})
	return r, nil
}

// Logger returns the current snapshot; level changes apply to later writes.
func (r *Runtime) Logger() *slog.Logger { return r.logger }

// SetLevel switches the level at runtime; unknown names are rejected.
func (r *Runtime) SetLevel(s string) error {
	l, e := strictLevel(s)
	if e == nil {
		r.level.Set(l)
	}
	return e
}

// ErrorCount reports output and lifecycle failures since construction, including
// background maintenance failures even before they are reported by a caller.
func (r *Runtime) ErrorCount() uint64 { return r.errors.Load() }
func (r *Runtime) write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		r.countError(os.ErrClosed)
		return 0, os.ErrClosed
	}
	var errs []error
	if r.file != nil {
		errs = append(errs, r.file.pendingMaintenanceError())
	}
	for _, w := range r.sinks {
		n, e := w.Write(p)
		if e == nil && n != len(p) {
			e = io.ErrShortWrite
		}
		if e != nil {
			r.countError(e)
			errs = append(errs, e)
		}
	}
	if e := errors.Join(errs...); e != nil {
		return 0, e
	}
	return len(p), nil
}

// Rotate reopens the output files; see the file handler for failure semantics.
func (r *Runtime) Rotate() error {
	r.mu.Lock()
	var err, background error
	if r.closed {
		err = os.ErrClosed
	} else if r.file != nil {
		err = r.file.rotate()
		background = r.file.pendingMaintenanceError()
	}
	r.countError(err)
	r.mu.Unlock()
	err = errors.Join(err, background)
	r.report(err)
	return err
}

// Close releases the active file and drains all queued maintenance. Concurrent
// and repeated callers receive the same result. It does not close stdout.
func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.closeDone
		return r.closeErr
	}
	r.closed = true
	var err error
	if r.file != nil {
		err = r.file.close()
	}
	r.countError(err)
	r.mu.Unlock()
	var pending error
	if r.file != nil {
		err = errors.Join(err, r.file.drainMaintenance())
		pending = r.file.pendingMaintenanceError()
	}
	r.closeErr = err
	close(r.closeDone)
	r.report(errors.Join(err, pending))
	return err
}

// Report only after handler and writer locks are released: callbacks may log,
// rotate or close. Recursive/concurrent reports are suppressed, never counted away.
func (r *Runtime) report(e error) {
	if e == nil || !r.reporting.CompareAndSwap(false, true) {
		return
	}
	defer r.reporting.Store(false)
	now := time.Now()
	if !r.lastReport.IsZero() && now.Sub(r.lastReport) < time.Second {
		return
	}
	r.lastReport = now
	defer func() { _ = recover() }()
	if r.onError != nil {
		r.onError(e)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "logger: %v\n", e)
	}
}

type reportHandler struct {
	slog.Handler
	r *Runtime
}

func (h reportHandler) Handle(c context.Context, rec slog.Record) error {
	e := h.Handler.Handle(c, rec)
	h.r.report(e)
	return e
}
func (h reportHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return reportHandler{h.Handler.WithAttrs(a), h.r}
}
func (h reportHandler) WithGroup(g string) slog.Handler {
	return reportHandler{h.Handler.WithGroup(g), h.r}
}

func (r *Runtime) countError(e error) {
	if e == nil {
		return
	}
	if joined, ok := e.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			r.countError(child)
		}
		return
	}
	r.errors.Add(1)
}

type runtimeWriter struct{ r *Runtime }

func (w runtimeWriter) Write(p []byte) (int, error) { return w.r.write(p) }
