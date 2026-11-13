package logger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelativeFilePathSurvivesChdir(t *testing.T) {
	// Chdir changes process-wide state; this test must not run in parallel.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	a, b := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(b, "app.log"), []byte("unrelated"), 0600))
	require.NoError(t, os.Chdir(a))
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	r, err := Open(RuntimeOptions{File: &FileOptions{Path: "app.log", Compress: ptr(false)}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })
	r.Logger().Info("before")
	require.NoError(t, os.Chdir(b))
	require.NoError(t, r.Rotate())
	r.Logger().Info("after")
	require.NoError(t, r.Close())
	foreign, err := os.ReadFile(filepath.Join(b, "app.log"))
	require.NoError(t, err)
	require.Equal(t, "unrelated", string(foreign))
	archives, err := filepath.Glob(filepath.Join(a, "app.log.archive-*"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	data, err := os.ReadFile(archives[0])
	require.NoError(t, err)
	require.Contains(t, string(data), "before")
	data, err = os.ReadFile(filepath.Join(a, "app.log"))
	require.NoError(t, err)
	require.Contains(t, string(data), "after")
}

const orphanArchive = "app.log.archive-20260928T120000.000000000Z-123"

func TestOwnedInterruptedGzipRecovered(t *testing.T) {
	for _, operation := range []string{"open", "rotate"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			opts := RuntimeOptions{File: &FileOptions{Path: filepath.Join(dir, "app.log"), Compress: ptr(false), MaxBackups: ptr(0), MaxAgeDays: ptr(0)}}
			var r *Runtime
			var err error
			if operation == "rotate" {
				r, err = Open(opts)
				require.NoError(t, err)
				defer func() { _ = r.Close() }()
			}
			source := filepath.Join(dir, orphanArchive)
			tmp := filepath.Join(dir, "."+orphanArchive+".logger-gzip-456")
			require.NoError(t, os.WriteFile(source, []byte("original records"), 0600))
			require.NoError(t, os.WriteFile(tmp, []byte("partial gzip"), 0600))
			if operation == "open" {
				r, err = Open(opts)
				require.NoError(t, err)
				defer func() { _ = r.Close() }()
			} else {
				require.NoError(t, r.Rotate())
			}
			waitMaintenance(r)
			require.NoFileExists(t, tmp)
			data, err := os.ReadFile(source)
			require.NoError(t, err)
			require.Equal(t, "original records", string(data))
		})
	}
}

func TestGzipRecoveryPreservesUnownedAndNonregular(t *testing.T) {
	dir := t.TempDir()
	names := []string{".logger-gzip-123", ".other.log.archive-20260928T120000.000000000Z-123.logger-gzip-456", ".app.log.archive-invalid.logger-gzip-456", "." + orphanArchive + ".logger-gzip-notnumeric"}
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0600))
	}
	nonregular := filepath.Join(dir, "."+orphanArchive+".logger-gzip-789")
	require.NoError(t, os.Mkdir(nonregular, 0700))
	link := filepath.Join(dir, "."+orphanArchive+".logger-gzip-890")
	linkCreated := os.Symlink(filepath.Join(dir, names[0]), link) == nil
	r, err := Open(RuntimeOptions{File: &FileOptions{Path: filepath.Join(dir, "app.log")}})
	require.NoError(t, err)
	require.NoError(t, r.Close())
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.Equal(t, "keep", string(data))
	}
	require.DirExists(t, nonregular)
	if linkCreated {
		info, err := os.Lstat(link)
		require.NoError(t, err)
		require.NotZero(t, info.Mode()&os.ModeSymlink)
	}
}
