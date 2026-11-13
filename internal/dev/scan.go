package dev

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const maxSourceBytes = 16 << 20
const maxSourceFiles = 10000
const maxScanEntries = 100000
const maxScanDepth = 64

func scan(root string) ([32]byte, error) {
	return scanContext(context.Background(), root)
}

func scanContext(ctx context.Context, root string) ([32]byte, error) {
	return scanWithBudget(ctx, root, maxScanEntries)
}

func scanWithBudget(ctx context.Context, root string, entryBudget int) ([32]byte, error) {
	h := sha256.New()
	var total int64
	count := 0
	visited := 0
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > maxScanDepth {
			return fmt.Errorf("source scan exceeds directory depth %d", maxScanDepth)
		}
		entries, err := readEntries(ctx, dir, &visited, entryBudget)
		if err != nil {
			return err
		}
		for _, d := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := filepath.Join(dir, d.Name())
			if d.Type()&os.ModeSymlink != 0 {
				continue
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".frame-dev", "logs", "bin", "vendor", "node_modules":
					continue
				}
				if err := walk(path, depth+1); err != nil {
					return err
				}
				continue
			}
			if !strings.HasSuffix(d.Name(), ".go") && d.Name() != "go.mod" && d.Name() != "go.sum" {
				continue
			}
			count++
			if count > maxSourceFiles {
				return fmt.Errorf("source scan exceeds %d files", maxSourceFiles)
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("source is not a regular file: %s", path)
			}
			total += info.Size()
			if total > maxSourceBytes {
				return fmt.Errorf("source scan exceeds %d bytes", maxSourceBytes)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(h, "%d:%s:%d:", len(rel), rel, info.Size())
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			n, copyErr := io.Copy(h, io.LimitReader(f, info.Size()+1))
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if n != info.Size() {
				return fmt.Errorf("source changed during scan: %s", path)
			}
		}
		return nil
	}
	err := walk(root, 0)
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest, err
}

// Read in chunks so a single large directory cannot allocate without a bound.
// Sorting makes fingerprints independent of filesystem enumeration order.
func readEntries(ctx context.Context, dir string, visited *int, budget int) (entries []os.DirEntry, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		batch, readErr := f.ReadDir(128)
		*visited += len(batch)
		if *visited > budget {
			return nil, fmt.Errorf("source scan exceeds %d directory entries", budget)
		}
		entries = append(entries, batch...)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}
