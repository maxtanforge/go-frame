package logger

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOwnedGzipCleanupFailureReported(t *testing.T) {
	dir := t.TempDir()
	opts := RuntimeOptions{File: &FileOptions{Path: filepath.Join(dir, "app.log")}, OnError: func(error) {}}
	r, err := Open(opts)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	tmp := filepath.Join(dir, "."+orphanArchive+".logger-gzip-456")
	require.NoError(t, os.WriteFile(tmp, []byte("partial"), 0600))
	name, err := syscall.UTF16PtrFromString(tmp)
	require.NoError(t, err)
	// Hold a real handle without FILE_SHARE_DELETE, preventing orphan removal.
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	defer func() { _ = syscall.CloseHandle(h) }()
	require.NoError(t, r.Rotate())
	waitMaintenance(r)
	require.Error(t, r.file.pendingMaintenanceError())
	require.Positive(t, r.ErrorCount())
	require.FileExists(t, tmp)
	require.Error(t, r.Close())
	other, err := Open(opts)
	require.Error(t, err)
	require.Nil(t, other)
}
