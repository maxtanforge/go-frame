// Package dev implements a bounded, build-before-stop development supervisor.
package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Options struct {
	Dir, Package                    string
	Args                            []string
	Interval, Debounce, StopTimeout time.Duration
	Stdout, Stderr                  io.Writer
}

type process interface {
	Done() <-chan struct{}
	Err() error
	Stop(time.Duration) error
}
type launcher func(string, string, []string) (process, error)

func Run(ctx context.Context, o Options) error {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return run(ctx, o, func(dir, program string, args []string) (process, error) {
		return startProcess(dir, program, args, o.Stdout, o.Stderr, o.StopTimeout)
	})
}

func run(ctx context.Context, o Options, start launcher) (result error) {
	if ctx == nil {
		return fmt.Errorf("nil context")
	}
	if o.Dir == "" || o.Package == "" || o.Interval <= 0 || o.Debounce < 0 || o.StopTimeout <= 0 {
		return fmt.Errorf("invalid dev options")
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	root, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	o.Dir = root
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("dev root is not a directory")
	}
	observed, err := scanContext(ctx, root)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return err
	}
	outputRoot := filepath.Join(root, ".frame-dev")
	if err := os.MkdirAll(outputRoot, 0700); err != nil {
		return err
	}
	// Never write through a preexisting output directory symlink.
	info, err = os.Lstat(outputRoot)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe dev output directory")
	}
	runDir, err := os.MkdirTemp(outputRoot, "run-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(runDir) }()
	var app process
	var appDone <-chan struct{}
	lastBinary := ""
	sequence := 0
	defer func() {
		if app != nil {
			if err := app.Stop(o.StopTimeout); err != nil {
				result = errors.Join(result, fmt.Errorf("stop app: %w", err))
			}
		}
	}()
	log := func(format string, args ...any) { _, _ = fmt.Fprintf(o.Stderr, "frame dev: "+format+"\n", args...) }
	pending := true
	scanFailed := false
	changed := time.Now().Add(-o.Debounce)
	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if pending && time.Since(changed) >= o.Debounce {
			pending = false
			sequence++
			candidate := filepath.Join(runDir, fmt.Sprintf("app-%06d", sequence))
			if runtime.GOOS == "windows" {
				candidate += ".exe"
			}
			log("building %s", o.Package)
			build, buildErr := start(root, "go", []string{"build", "-buildvcs=false", "-o", candidate, "--", o.Package})
			if buildErr == nil {
				select {
				case <-ctx.Done():
					return build.Stop(o.StopTimeout)
				case <-build.Done():
					buildErr = build.Err()
				}
			}
			latest, scanErr := scanContext(ctx, root)
			if ctx.Err() != nil {
				_ = os.Remove(candidate)
				return nil
			}
			if scanErr != nil {
				_ = os.Remove(candidate)
				log("source scan failed; previous app retained: %v", scanErr)
				scanFailed = true
				continue
			}
			if latest != observed {
				observed = latest
				pending = true
				changed = time.Now()
				_ = os.Remove(candidate)
				log("source changed during build; discarding candidate")
				continue
			}
			if buildErr != nil {
				_ = os.Remove(candidate)
				log("build failed; previous app retained: %v", buildErr)
			} else {
				if app != nil {
					if err := app.Stop(o.StopTimeout); err != nil {
						_ = os.Remove(candidate)
						return fmt.Errorf("stop previous app: %w", err)
					}
					app = nil
					appDone = nil
				}
				next, startErr := start(root, candidate, o.Args)
				if startErr != nil {
					_ = os.Remove(candidate)
					log("app start failed; waiting for a source change: %v", startErr)
				} else {
					if lastBinary != "" {
						_ = os.Remove(lastBinary)
					}
					lastBinary = candidate
					app = next
					appDone = next.Done()
					log("started %s", filepath.Base(candidate))
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-appDone:
			log("app exited (%v); waiting for a source change", app.Err())
			appDone = nil
		case <-ticker.C:
			latest, err := scanContext(ctx, root)
			if err != nil {
				if !scanFailed {
					log("source scan failed; previous app retained: %v", err)
				}
				scanFailed = true
				pending = false
				continue
			}
			if scanFailed || latest != observed {
				scanFailed = false
				observed = latest
				pending = true
				changed = time.Now()
			}
		}
	}
}
