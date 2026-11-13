package logger

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }
func TestRuntimeValidation(t *testing.T) {
	for _, o := range []RuntimeOptions{{Options: Options{Level: "oops"}}, {File: &FileOptions{}}, {File: &FileOptions{Path: t.TempDir()}}, {File: &FileOptions{Path: "a", MaxSizeMB: -1}}, {File: &FileOptions{Path: "a", MaxBackups: ptr(-1)}}, {File: &FileOptions{Path: "a", MaxAgeDays: ptr(-1)}}, {File: &FileOptions{Path: "a", Timezone: "missing/zone"}}} {
		r, e := Open(o)
		require.Error(t, e)
		require.Nil(t, r)
	}
}
func TestRuntimeLosslessRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "app.log")
	r, e := Open(RuntimeOptions{Options: Options{Format: "json"}, File: &FileOptions{Path: path, MaxSizeMB: 1, MaxBackups: ptr(0), MaxAgeDays: ptr(0)}})
	require.NoError(t, e)
	for i := 0; i < 3000; i++ {
		r.Logger().Info(strings.Repeat("x", 1000), "i", i)
	}
	require.NoError(t, r.Rotate())
	require.NoError(t, r.Close())
	require.NoError(t, r.Close())
	files, e := filepath.Glob(path + "*")
	require.NoError(t, e)
	require.Greater(t, len(files), 3)
	seen := map[int]bool{}
	for _, p := range files {
		data, e := os.ReadFile(p)
		require.NoError(t, e)
		if strings.HasSuffix(p, ".gz") {
			z, e := gzip.NewReader(bytes.NewReader(data))
			require.NoError(t, e)
			data, e = io.ReadAll(z)
			require.NoError(t, e)
			require.NoError(t, z.Close())
		}
		require.LessOrEqual(t, len(data), 1024*1024)
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var rec struct {
				I int `json:"i"`
			}
			require.NoError(t, json.Unmarshal(line, &rec))
			require.False(t, seen[rec.I])
			seen[rec.I] = true
		}
	}
	require.Len(t, seen, 3000)
	require.Zero(t, r.ErrorCount())
	r.Logger().Info("after close")
	require.Equal(t, uint64(1), r.ErrorCount())
	require.Error(t, r.Rotate())
}
func TestRuntimeLevelAndOversize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	r, e := Open(RuntimeOptions{File: &FileOptions{Path: p, MaxSizeMB: 1}, OnError: func(error) {}})
	require.NoError(t, e)
	defer func() { _ = r.Close() }()
	require.NoError(t, r.SetLevel("error"))
	r.Logger().Info("hidden")
	r.Logger().Error("visible")
	require.Error(t, r.SetLevel("invalid"))
	r.Logger().Error(strings.Repeat("x", 1024*1024))
	require.Equal(t, uint64(1), r.ErrorCount())
	b, e := os.ReadFile(p)
	require.NoError(t, e)
	require.NotContains(t, string(b), "hidden")
	require.Contains(t, string(b), "visible")
	require.Less(t, len(b), 1024)
}
func TestRuntimeConcurrentClose(t *testing.T) {
	r, e := Open(RuntimeOptions{File: &FileOptions{Path: filepath.Join(t.TempDir(), "app.log")}, OnError: func(error) {}})
	require.NoError(t, e)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Logger().Info("hello")
				_ = r.Close()
			}
		}()
	}
	wg.Wait()
	require.NoError(t, r.Close())
}
func TestRuntimeRetentionAndTimezone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	r, e := Open(RuntimeOptions{File: &FileOptions{Path: p, MaxBackups: ptr(2), MaxAgeDays: ptr(1), Compress: ptr(false), Timezone: "America/Chicago"}})
	require.NoError(t, e)
	for i := 0; i < 5; i++ {
		r.Logger().Info("hello")
		require.NoError(t, r.Rotate())
	}
	waitMaintenance(r)
	files, _ := filepath.Glob(p + ".archive-*")
	require.Len(t, files, 2)
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(files[0], old, old))
	foreign := p + ".archive-unrelated"
	require.NoError(t, os.WriteFile(foreign, []byte("keep"), 0600))
	require.NoError(t, r.Rotate())
	waitMaintenance(r)
	_, e = os.Stat(files[0])
	require.True(t, errors.Is(e, os.ErrNotExist))
	_, e = os.Stat(foreign)
	require.NoError(t, e)
	require.NoError(t, r.Close())
}

type brokenWriter struct{ short bool }

func (w brokenWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("broken sink")
}
func TestRuntimeIndependentSinksAndReentrantReport(t *testing.T) {
	var r *Runtime
	calls := 0
	var e error
	r, e = Open(RuntimeOptions{File: &FileOptions{Path: filepath.Join(t.TempDir(), "app.log")}, OnError: func(error) { calls++; r.Logger().Error("recursive"); require.NoError(t, r.Close()); panic("isolated") }})
	require.NoError(t, e)
	var good bytes.Buffer
	r.sinks = []io.Writer{brokenWriter{}, brokenWriter{short: true}, &good}
	done := make(chan struct{})
	go func() { r.Logger().Info("outer"); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("callback deadlock")
	}
	require.Equal(t, 1, calls)
	require.Equal(t, uint64(4), r.ErrorCount())
	require.Contains(t, good.String(), "outer")
	require.Contains(t, good.String(), "recursive")
}
func TestRotationFailureDoesNotExceedLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	r, e := Open(RuntimeOptions{File: &FileOptions{Path: p, MaxSizeMB: 1}, OnError: func(error) {}})
	require.NoError(t, e)
	defer func() { _ = r.Close() }()
	r.Logger().Info(strings.Repeat("a", 700000))
	r.file.path = filepath.Join(t.TempDir(), "missing", "app.log")
	r.Logger().Info(strings.Repeat("b", 700000))
	s, e := os.Stat(p)
	require.NoError(t, e)
	require.LessOrEqual(t, s.Size(), int64(1024*1024))
	require.Equal(t, uint64(1), r.ErrorCount())
}
func TestCompressionFailurePreservesOriginal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "original")
	require.NoError(t, os.WriteFile(p, []byte("original"), 0600))
	require.NoError(t, os.Mkdir(p+".gz", 0700))
	require.Error(t, compressArchive(p))
	b, e := os.ReadFile(p)
	require.NoError(t, e)
	require.Equal(t, "original", string(b))
}
func TestRuntimeRestartUniqueArchives(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	for i := 0; i < 12; i++ {
		r, e := Open(RuntimeOptions{File: &FileOptions{Path: p, Compress: ptr(false), MaxBackups: ptr(0), Timezone: "America/Chicago"}})
		require.NoError(t, e)
		r.Logger().Info("entry", "i", i)
		require.NoError(t, r.Rotate())
		require.NoError(t, r.Close())
	}
	files, _ := filepath.Glob(p + ".archive-*")
	require.Len(t, files, 12)
	for _, p := range files {
		require.Regexp(t, `-0[56]00-`, p)
	}
}
func TestRuntimeStdoutSurvivesClose(t *testing.T) {
	old := os.Stdout
	f, e := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, e)
	os.Stdout = f
	defer func() { os.Stdout = old; _ = f.Close() }()
	r, e := Open(RuntimeOptions{})
	require.NoError(t, e)
	r.Logger().Info("stdout message")
	require.NoError(t, r.Close())
	_, e = f.WriteString("still open")
	require.NoError(t, e)
	b, e := os.ReadFile(f.Name())
	require.NoError(t, e)
	require.Contains(t, string(b), "stdout message")
	require.Contains(t, string(b), "still open")
}

type joinedFailureWriter struct{}

func (joinedFailureWriter) Write([]byte) (int, error) {
	return 0, errors.Join(errors.New("first failure"), errors.New("second failure"))
}
func TestRuntimeCountsEveryMaintenanceFailure(t *testing.T) {
	r, e := Open(RuntimeOptions{OnError: func(error) {}})
	require.NoError(t, e)
	defer func() { _ = r.Close() }()
	r.sinks = []io.Writer{joinedFailureWriter{}}
	r.Logger().Info("record")
	require.Equal(t, uint64(2), r.ErrorCount())
}
func TestRuntimeMaintainsArchivesOnOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	r, e := Open(RuntimeOptions{File: &FileOptions{Path: p, Compress: ptr(false), MaxBackups: ptr(0)}})
	require.NoError(t, e)
	for i := 0; i < 4; i++ {
		r.Logger().Info("record")
		require.NoError(t, r.Rotate())
	}
	require.NoError(t, r.Close())
	r, e = Open(RuntimeOptions{File: &FileOptions{Path: p, MaxBackups: ptr(1)}})
	require.NoError(t, e)
	defer func() { _ = r.Close() }()
	waitMaintenance(r)
	files, _ := filepath.Glob(p + ".archive-*")
	require.Len(t, files, 1)
}
