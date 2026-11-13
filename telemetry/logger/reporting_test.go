package logger

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeReportsRealMaintenanceFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	var reported error
	r, err := Open(RuntimeOptions{File: &FileOptions{Path: path}, OnError: func(err error) { reported = err }})
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	// An empty rotation enters retention directly. Point its directory at an
	// existing regular file to cause a real ReadDir failure on every platform.
	r.file.path = filepath.Join(path, "child.log")
	require.NoError(t, r.Rotate())
	waitMaintenance(r)
	err = writeRecord(r, "trigger report")
	r.report(err)
	require.Error(t, err)
	var pathErr *os.PathError
	require.True(t, errors.As(err, &pathErr))
	require.Equal(t, filepath.Clean(path), filepath.Clean(pathErr.Path))
	require.Equal(t, uint64(1), r.ErrorCount())
	require.ErrorIs(t, reported, err)
	// Maintenance failure must not damage the already-open active file.
	r.Logger().Info("still writable")
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(contents), "still writable")
}

func TestRuntimeRateLimitCountsSuppressedFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		r, err := Open(RuntimeOptions{OnError: func(error) { calls++ }})
		require.NoError(t, err)
		defer func() { _ = r.Close() }()
		r.sinks = []io.Writer{brokenWriter{}}
		r.Logger().Info("first")
		require.Equal(t, 1, calls)
		time.Sleep(999 * time.Millisecond)
		r.Logger().Info("suppressed")
		require.Equal(t, 1, calls)
		require.Equal(t, uint64(2), r.ErrorCount())
		time.Sleep(time.Millisecond)
		r.Logger().Info("reported after interval")
		require.Equal(t, 2, calls)
		require.Equal(t, uint64(3), r.ErrorCount())
	})
}
