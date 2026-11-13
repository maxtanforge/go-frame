//go:build linux

package logger

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenRejectsFIFOPromptly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, syscall.Mkfifo(path, 0600))
	done := make(chan error, 1)
	go func() {
		r, err := Open(RuntimeOptions{File: &FileOptions{Path: path}})
		if r != nil {
			_ = r.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		// Release a regressed blocking writer before failing the bounded test.
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			defer func() {
				if err := f.Close(); err != nil {
					t.Errorf("close FIFO reader: %v", err)
				}
			}()
		}
		t.Fatal("Open blocked on FIFO")
	}
}
