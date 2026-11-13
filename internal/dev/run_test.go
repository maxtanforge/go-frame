package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProcess struct {
	done    chan struct{}
	err     error
	stopErr error
	once    sync.Once
	stops   atomic.Int32
}

func newFake() *fakeProcess                  { return &fakeProcess{done: make(chan struct{})} }
func (p *fakeProcess) Done() <-chan struct{} { return p.done }
func (p *fakeProcess) Err() error            { return p.err }
func (p *fakeProcess) Stop(time.Duration) error {
	p.stops.Add(1)
	p.once.Do(func() { close(p.done) })
	return p.stopErr
}
func TestBuildFailureRetainsAppAndStaleBuildIsDiscarded(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	_ = os.WriteFile(source, []byte("one"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	app := newFake()
	builds := 0
	starts := 0
	failed := make(chan struct{})
	cfg := Options{Dir: root, Package: ".", Interval: time.Millisecond, Debounce: time.Millisecond, StopTimeout: time.Second, Stdout: io.Discard, Stderr: io.Discard}
	launch := func(_, program string, _ []string) (process, error) {
		if program == "go" {
			builds++
			p := newFake()
			switch builds {
			case 2:
				p.err = errors.New("compile error")
				close(failed)
			case 3:
				_ = os.WriteFile(source, []byte("four"), 0600)
			}
			p.once.Do(func() { close(p.done) })
			return p, nil
		}
		starts++
		if starts == 1 {
			go func() {
				time.Sleep(10 * time.Millisecond)
				if err := atomicSource(source, "two"); err != nil {
					t.Error(err)
					cancel()
					return
				}
				select {
				case <-failed:
				case <-ctx.Done():
					return
				}
				time.Sleep(10 * time.Millisecond)
				if app.stops.Load() != 0 {
					t.Error("failed build stopped old app")
				}
				if err := atomicSource(source, "three"); err != nil {
					t.Error(err)
					cancel()
					return
				}
			}()
			return app, nil
		}
		if builds != 4 {
			t.Errorf("started stale candidate: builds=%d", builds)
		}
		cancel()
		return newFake(), nil
	}
	if err := run(ctx, cfg, launch); err != nil {
		t.Fatal(err)
	}
	if starts != 2 || builds != 4 {
		t.Fatalf("starts=%d builds=%d", starts, builds)
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".frame-dev"))
	if len(entries) != 0 {
		t.Fatal("run directory leaked")
	}
}

func TestAppExitDoesNotRestartLoop(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("one"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	starts := 0
	cfg := Options{Dir: root, Package: ".", Interval: time.Millisecond, Debounce: time.Millisecond, StopTimeout: time.Second, Stdout: io.Discard, Stderr: io.Discard}
	err := run(ctx, cfg, func(_, program string, _ []string) (process, error) {
		p := newFake()
		p.once.Do(func() { close(p.done) })
		if program != "go" {
			starts++
			time.AfterFunc(30*time.Millisecond, cancel)
		}
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("restart loop: %d", starts)
	}
}

func atomicSource(path, body string) error {
	if err := os.WriteFile(path+".tmp", []byte(body), 0600); err != nil {
		return err
	}
	var err error
	for i := 0; i < 100; i++ {
		err = os.Rename(path+".tmp", path)
		if err == nil {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return err
}

type logWriterFunc func([]byte) (int, error)

func (f logWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestScanFailureRetainsRunningApp(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	_ = os.WriteFile(source, []byte("one"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	app := newFake()
	builds := 0
	starts := 0
	checked := make(chan struct{})
	scanFailed := make(chan struct{}, 1)
	stderr := logWriterFunc(func(p []byte) (int, error) {
		if strings.Contains(string(p), "source scan failed; previous app retained:") {
			select {
			case scanFailed <- struct{}{}:
			default:
			}
		}
		return len(p), nil
	})
	cfg := Options{Dir: root, Package: ".", Interval: time.Millisecond, Debounce: time.Millisecond, StopTimeout: time.Second, Stdout: io.Discard, Stderr: stderr}
	err := run(ctx, cfg, func(_, program string, _ []string) (process, error) {
		if program == "go" {
			builds++
			p := newFake()
			p.once.Do(func() { close(p.done) })
			return p, nil
		}
		starts++
		if starts == 1 {
			large := filepath.Join(root, "large.go")
			if err := os.WriteFile(large, make([]byte, maxSourceBytes+1), 0600); err != nil {
				t.Fatal(err)
			}
			go func() {
				defer close(checked)
				// Keep the invalid source until the supervisor actually observes it.
				select {
				case <-scanFailed:
				case <-ctx.Done():
					t.Error("source scan failure not observed")
					return
				}
				if app.stops.Load() != 0 {
					t.Error("scan error stopped running app")
				}
				if err := os.Remove(large); err != nil {
					t.Error(err)
					cancel()
				}
			}()
			return app, nil
		}
		cancel()
		return newFake(), nil
	})
	cancel()
	if starts > 0 {
		<-checked
	}
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-checked:
	default:
		t.Fatal("scan recovery not checked")
	}
	if starts != 2 || builds != 2 {
		t.Fatalf("starts=%d builds=%d", starts, builds)
	}
}

func TestCancellationStopsBuild(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	build := newFake()
	cfg := Options{Dir: root, Package: ".", Interval: time.Millisecond, StopTimeout: time.Second, Stdout: io.Discard, Stderr: io.Discard}
	err := run(ctx, cfg, func(_, program string, _ []string) (process, error) {
		if program != "go" {
			t.Fatal("app started")
		}
		cancel()
		return build, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if build.stops.Load() != 1 {
		t.Fatal("build not stopped")
	}
}

func TestAppStartFailureWaitsForChange(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	starts := 0
	cfg := Options{Dir: root, Package: ".", Interval: time.Millisecond, StopTimeout: time.Second, Stdout: io.Discard, Stderr: io.Discard}
	err := run(ctx, cfg, func(_, program string, _ []string) (process, error) {
		if program != "go" {
			starts++
			time.AfterFunc(30*time.Millisecond, cancel)
			return nil, errors.New("start failed")
		}
		p := newFake()
		p.once.Do(func() { close(p.done) })
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("startup failure loop: %d", starts)
	}
}

func TestStopFailurePreventsReplacement(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	_ = os.WriteFile(source, []byte("one"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopErr := errors.New("stop deadline exceeded")
	app := newFake()
	app.stopErr = stopErr
	starts := 0
	cfg := Options{Dir: root, Package: ".", Interval: time.Millisecond, StopTimeout: time.Millisecond, Stdout: io.Discard, Stderr: io.Discard}
	err := run(ctx, cfg, func(_, program string, _ []string) (process, error) {
		if program == "go" {
			p := newFake()
			p.once.Do(func() { close(p.done) })
			return p, nil
		}
		starts++
		_ = os.WriteFile(source, []byte("two"), 0600)
		return app, nil
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("stop failure lost: %v", err)
	}
	if starts != 1 {
		t.Fatalf("replacement started despite failed stop: %d", starts)
	}
}

func TestCanceledShutdownPropagatesStopError(t *testing.T) {
	for _, duringBuild := range []bool{false, true} {
		t.Run(fmt.Sprint(duringBuild), func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopErr := errors.New("stop deadline exceeded")
			child := newFake()
			child.stopErr = stopErr
			cfg := Options{Dir: root, Package: ".", Interval: time.Millisecond, StopTimeout: time.Millisecond, Stdout: io.Discard, Stderr: io.Discard}
			err := run(ctx, cfg, func(_, program string, _ []string) (process, error) {
				if program == "go" && !duringBuild {
					p := newFake()
					p.once.Do(func() { close(p.done) })
					return p, nil
				}
				cancel()
				return child, nil
			})
			if !errors.Is(err, stopErr) {
				t.Fatalf("shutdown stop failure lost: %v", err)
			}
		})
	}
}
