package reload

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxFiles = 32
const maxFileBytes = 1 << 20

// WatchOptions selects a bounded list of explicit files. Zero timing values use
// defaults (500 ms polling, 300 ms debounce); intervals below 10 ms are invalid.
// Callbacks run serially on the calling goroutine, must not panic, and must
// cooperate with cancellation. OnError receives only sanitized errors.
type WatchOptions struct {
	Paths              []string
	Interval, Debounce time.Duration
	OnChange           func(context.Context) error
	OnError            func(error)
}

type fileState struct {
	Hash    [sha256.Size]byte
	Problem string
}
type observation [maxFiles]fileState

// Watch blocks until cancellation. It reconciles once on a healthy initial
// observation, then invokes OnChange after stable content changes. Missing or
// unreadable files retain the service's last configuration and recover on later
// polls. Symlinks are followed; directory and special-file targets are rejected
// before opening. Callers must control the watched paths and their directories.
// File operations and callbacks are cooperative, not forcibly interrupted.
func Watch(ctx context.Context, options WatchOptions) error {
	if ctx == nil {
		return errors.New("reload: nil context")
	}
	if len(options.Paths) == 0 || len(options.Paths) > maxFiles || options.OnChange == nil {
		return errors.New("reload: invalid watcher options")
	}
	if options.Interval == 0 {
		options.Interval = 500 * time.Millisecond
	}
	if options.Debounce == 0 {
		options.Debounce = 300 * time.Millisecond
	}
	if options.Interval < 10*time.Millisecond || options.Debounce < 0 {
		return errors.New("reload: invalid watcher timing")
	}
	paths := make([]string, len(options.Paths))
	seen := map[string]bool{}
	for i, p := range options.Paths {
		if strings.TrimSpace(p) == "" {
			return errors.New("reload: empty watch path")
		}
		absolute, err := filepath.Abs(p)
		if err != nil {
			return errors.New("reload: invalid watch path")
		}
		key := absolute
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return errors.New("reload: duplicate watch path")
		}
		seen[key] = true
		paths[i] = absolute
	}
	if ctx.Err() != nil {
		return nil
	}
	previous := observe(ctx, paths)
	reportProblems(options, observation{}, previous, len(paths))
	invoke := func() {
		if ctx.Err() != nil {
			return
		}
		if err := options.OnChange(ctx); err != nil && ctx.Err() == nil && options.OnError != nil {
			options.OnError(safeError("watch callback", err))
		}
	}
	if healthy(previous, len(paths)) {
		invoke()
	}
	ticker := time.NewTicker(options.Interval)
	defer ticker.Stop()
	pending := false
	var changedAt time.Time
	for {
		if ctx.Err() != nil {
			return nil
		}
		// In particular, reobserve immediately after a callback. The baseline stays
		// the pre-callback observation so edits during loading cannot disappear.
		current := observe(ctx, paths)
		now := time.Now()
		if current != previous {
			reportProblems(options, previous, current, len(paths))
			previous = current
			pending = true
			changedAt = now
		}
		if pending && healthy(current, len(paths)) && now.Sub(changedAt) >= options.Debounce {
			pending = false
			invoke()
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func observe(ctx context.Context, paths []string) observation {
	var result observation
	for i, path := range paths {
		if ctx.Err() != nil {
			break
		}
		result[i] = observeFile(path)
	}
	return result
}

func observeFile(path string) (st fileState) {
	info, err := os.Stat(path)
	if err != nil {
		return fileState{Problem: fileProblem(err)}
	}
	if !info.Mode().IsRegular() {
		return fileState{Problem: "not a regular file"}
	}
	if info.Size() > maxFileBytes {
		return fileState{Problem: "file exceeds size limit"}
	}
	file, err := openWatchFile(path)
	if err != nil {
		return fileState{Problem: fileProblem(err)}
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && st.Problem == "" {
			st.Problem = "file close failed"
		}
	}()
	info, err = file.Stat()
	if err != nil {
		return fileState{Problem: "file read failed"}
	}
	if !info.Mode().IsRegular() {
		return fileState{Problem: "not a regular file"}
	}
	if info.Size() > maxFileBytes {
		return fileState{Problem: "file exceeds size limit"}
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return fileState{Problem: "file read failed"}
	}
	if len(data) > maxFileBytes {
		return fileState{Problem: "file exceeds size limit"}
	}
	return fileState{Hash: sha256.Sum256(data)}
}

func fileProblem(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "file unavailable"
	}
	if errors.Is(err, os.ErrPermission) {
		return "file access denied"
	}
	return "file read failed"
}

func healthy(state observation, n int) bool {
	for _, s := range state[:n] {
		if s.Problem != "" {
			return false
		}
	}
	return true
}
func reportProblems(options WatchOptions, previous, current observation, n int) {
	if options.OnError == nil {
		return
	}
	for i, s := range current[:n] {
		if s.Problem != "" && s != previous[i] {
			options.OnError(fmt.Errorf("reload: watch file %d: %s", i+1, s.Problem))
		}
	}
}
