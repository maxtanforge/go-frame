package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeRecoversUnavailableFile(t *testing.T) {
	for _, operation := range []string{"write", "rotate"} {
		t.Run(operation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.log")
			r, err := Open(RuntimeOptions{File: &FileOptions{Path: path, Compress: ptr(false)}, OnError: func(error) {}})
			require.NoError(t, err)
			defer func() { _ = r.Close() }()
			r.Logger().Info("before failure")
			// Reproduce the post-rename replacement-open phase with a real obstruction.
			require.NoError(t, r.file.close())
			archive := path + ".archive-20260928T000000.000000000Z-1234"
			require.NoError(t, os.Rename(path, archive))
			require.NoError(t, os.Mkdir(path, 0700))
			require.Error(t, r.file.open())
			r.Logger().Info("unavailable")
			require.Equal(t, uint64(1), r.ErrorCount())
			require.NoError(t, os.Remove(path))
			if operation == "rotate" {
				require.NoError(t, r.Rotate())
			}
			r.Logger().Info("after recovery")
			b, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(b), "after recovery")
			require.NotContains(t, string(b), "before failure")
			b, err = os.ReadFile(archive)
			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(string(b), "before failure"))
			files, err := filepath.Glob(path + ".archive-*")
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.NoError(t, r.Close())
			require.NoError(t, os.Remove(path))
			r.Logger().Info("closed")
			require.Error(t, r.Rotate())
			_, err = os.Stat(path)
			require.True(t, os.IsNotExist(err))
		})
	}
}
func TestCompressionFailureStillRunsRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	w, err := openFile(FileOptions{Path: path, MaxBackups: ptr(1)})
	require.NoError(t, err)
	defer func() { _ = w.close() }()
	old := path + ".archive-20260927T000000.000000000Z-1234"
	current := path + ".archive-20260928T000000.000000000Z-1234"
	require.NoError(t, os.WriteFile(old, []byte("old"), 0600))
	require.NoError(t, os.WriteFile(current, []byte("preserve"), 0600))
	require.NoError(t, os.Mkdir(current+".gz", 0700))
	require.Error(t, w.finishRotation(current))
	_, err = os.Stat(old)
	require.True(t, os.IsNotExist(err))
	b, err := os.ReadFile(current)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(b))
}
func TestRecoveryRunsPendingRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	r, err := Open(RuntimeOptions{File: &FileOptions{Path: path, MaxBackups: ptr(1), Compress: ptr(false)}})
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	require.NoError(t, r.file.close())
	for _, stamp := range []string{"20260927", "20260928"} {
		require.NoError(t, os.WriteFile(path+".archive-"+stamp+"T000000.000000000Z-1234", []byte("record"), 0600))
	}
	r.Logger().Info("recovered")
	waitMaintenance(r)
	files, err := filepath.Glob(path + ".archive-*")
	require.NoError(t, err)
	require.Len(t, files, 1)
}
