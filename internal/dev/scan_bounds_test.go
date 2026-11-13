package dev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanBoundsNonSourceEntries(t *testing.T) {
	root := t.TempDir()
	for i := range 12 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("asset-%d.txt", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scanWithBudget(context.Background(), root, 10); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("non-source entries were not bounded: %v", err)
	}
}

func TestScanCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanContext(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan: %v", err)
	}
}
