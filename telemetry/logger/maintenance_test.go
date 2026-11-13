package logger

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeCompressionDoesNotHoldWriteLock(t *testing.T) {
	r, err := Open(RuntimeOptions{File: &FileOptions{Path: filepath.Join(t.TempDir(), "app.log")}, OnError: func(error) {}})
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	r.file.compressFn = func(path string) error { close(entered); <-release; return compressArchive(path) }
	defer func() { close(release); _ = r.Close() }()
	require.NoError(t, writeRecord(r, "before"))
	rotated := make(chan error, 1)
	go func() { rotated <- r.Rotate() }()
	<-entered
	written := make(chan error, 1)
	go func() { written <- writeRecord(r, "during compression") }()
	select {
	case err := <-written:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("compression held the runtime write lock")
	}
	select {
	case err := <-rotated:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Rotate waited for compression")
	}
}

func writeRecord(r *Runtime, text string) error { _, err := r.write([]byte(text)); return err }

func TestRuntimeCloseDrainsCompressionAndReturnsFailure(t *testing.T) {
	r, err := Open(RuntimeOptions{File: &FileOptions{Path: filepath.Join(t.TempDir(), "app.log")}, OnError: func(error) {}})
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("compression failed")
	r.file.compressFn = func(string) error { close(entered); <-release; return failure }
	require.NoError(t, writeRecord(r, "before"))
	require.NoError(t, r.Rotate())
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- r.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned before compression completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.ErrorIs(t, <-closed, failure)
	require.Equal(t, uint64(1), r.ErrorCount())
}

// Wait for all previously submitted jobs without relying on disk timing.
func waitMaintenance(r *Runtime) {
	done := make(chan struct{})
	r.mu.Lock()
	r.file.maintenance.jobs <- maintenanceJob{done: done}
	r.mu.Unlock()
	<-done
}

func TestRuntimeMaintenanceQueueAppliesBoundedBackpressure(t *testing.T) {
	r, err := Open(RuntimeOptions{File: &FileOptions{Path: filepath.Join(t.TempDir(), "app.log"), MaxBackups: ptr(1)}, OnError: func(error) {}})
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	var started, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	defer func() { unblock(); _ = r.Close() }()
	r.file.compressFn = func(path string) error {
		started.Do(func() { close(entered) })
		<-release
		return compressArchive(path)
	}
	require.NoError(t, writeRecord(r, "first"))
	require.NoError(t, r.Rotate())
	<-entered
	for range maintenanceQueueSize {
		require.NoError(t, writeRecord(r, "queued"))
		require.NoError(t, r.Rotate())
	}
	require.Len(t, r.file.maintenance.jobs, maintenanceQueueSize)
	require.NoError(t, writeRecord(r, "backpressure"))
	rotated := make(chan error, 1)
	go func() { rotated <- r.Rotate() }()
	require.Eventually(t, func() bool {
		m := r.file.maintenance
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.pending) == maintenanceQueueSize+2
	}, time.Second, time.Millisecond)
	select {
	case <-rotated:
		t.Fatal("full maintenance queue did not apply backpressure")
	default:
	}
	unblock()
	require.NoError(t, <-rotated)
	require.NoError(t, r.Close())
	require.Zero(t, r.ErrorCount())
	archives, err := filepath.Glob(r.file.path + ".archive-*")
	require.NoError(t, err)
	require.Len(t, archives, 1)
}

func TestRuntimeBackgroundErrorReportCanClose(t *testing.T) {
	var r *Runtime
	var err error
	reported := make(chan error, 1)
	r, err = Open(RuntimeOptions{File: &FileOptions{Path: filepath.Join(t.TempDir(), "app.log")}, OnError: func(error) { reported <- r.Close() }})
	require.NoError(t, err)
	failure := errors.New("compression failed")
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer func() {
		unblock()
		_ = r.Close()
	}()
	r.file.compressFn = func(string) error {
		<-release
		return failure
	}
	require.NoError(t, writeRecord(r, "first"))
	require.NoError(t, r.Rotate())
	unblock()
	waitMaintenance(r)
	r.Logger().Info("reports worker error")
	require.ErrorIs(t, <-reported, failure)
	require.Equal(t, uint64(1), r.ErrorCount())
	require.ErrorIs(t, r.Close(), failure)
}
