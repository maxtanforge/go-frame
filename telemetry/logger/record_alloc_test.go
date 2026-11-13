//go:build !race

package logger

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// The race runtime intentionally drops some sync.Pool puts, which adds scratch allocations.
func TestErrorStackAllocationBudget(t *testing.T) {
	h := stackHandler{Handler: slog.NewJSONHandler(io.Discard, nil)}
	record := slog.NewRecord(time.Now(), slog.LevelError, "failure", 0)
	allocations := testing.AllocsPerRun(100, func() {
		if err := h.Handle(context.Background(), record); err != nil {
			panic(err)
		}
	})
	if allocations > 1 {
		t.Fatalf("stack capture allocated %.0f times per record; want at most one owned stack string", allocations)
	}
}
