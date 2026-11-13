//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package reload

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestObserveRejectsFIFOWithoutOpening(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan fileState, 1)
	go func() { done <- observeFile(p) }()
	if state := waitEvent(t, done); state.Problem != "not a regular file" {
		t.Fatal(state)
	}
}
