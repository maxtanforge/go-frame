package reload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}

// Model an editor retrying Windows' transient sharing/access-denied response.
// The watcher must still observe the completed replacement without restarting.
func replaceFile(t *testing.T, source, target string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Rename(source, target)
		if err == nil {
			return
		}
		if runtime.GOOS != "windows" || !os.IsPermission(err) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func waitEvent[T any](t *testing.T, c <-chan T) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("event timeout")
		var zero T
		return zero
	}
}
func noEvent[T any](t *testing.T, c <-chan T, d time.Duration) {
	t.Helper()
	select {
	case v := <-c:
		t.Fatalf("unexpected event: %v", v)
	case <-time.After(d):
	}
}
func startWatch(t *testing.T, o WatchOptions) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, o) }()
	t.Cleanup(cancel)
	return cancel, done
}

func TestWatchDebouncesContentAndAtomicReplacement(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "old")
	// 虚拟时间让写入间隔(20ms)与 debounce(70ms)的关系确定:宿主负载拉伸
	// 真实时钟时,墙钟 sleep 一旦超过 debounce 就会触发中间回调(并行偶发失败根源)。
	// events 必须在 bubble 内创建:等待 bubble 外的 channel 属于等待外部世界,
	// 会让该 goroutine 非确定阻塞,虚拟时钟永不推进。文件 I/O 在真实世界完成,不参与虚拟时钟。
	synctest.Test(t, func(t *testing.T) {
		events := make(chan string, 10)
		cancel, done := startWatch(t, WatchOptions{Paths: []string{p}, Interval: 10 * time.Millisecond, Debounce: 70 * time.Millisecond, OnChange: func(context.Context) error { b, err := os.ReadFile(p); events <- string(b); return err }})
		if got := waitEvent(t, events); got != "old" {
			t.Fatal(got)
		}
		info, _ := os.Stat(p)
		for _, v := range []string{"one", "two", "end"} {
			writeFile(t, p, v)
			if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if got := waitEvent(t, events); got != "end" {
			t.Fatal(got)
		}
		noEvent(t, events, 100*time.Millisecond)
		replacement := p + ".new"
		writeFile(t, replacement, "replacement")
		replaceFile(t, replacement, p)
		if got := waitEvent(t, events); got != "replacement" {
			t.Fatal(got)
		}
		cancel()
		if err := waitEvent(t, done); err != nil {
			t.Fatal(err)
		}
	})
}
func TestWatchReportsRemovalOversizeAndRecoverySafely(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret-token")
	writeFile(t, p, "old")
	events := make(chan struct{}, 10)
	errs := make(chan error, 10)
	cancel, done := startWatch(t, WatchOptions{Paths: []string{p}, Interval: 10 * time.Millisecond, Debounce: 20 * time.Millisecond, OnChange: func(context.Context) error { events <- struct{}{}; return nil }, OnError: func(err error) { errs <- err }})
	waitEvent(t, events)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	err := waitEvent(t, errs)
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatal(err)
	}
	// Windows can briefly report delete-pending as access denied before absent.
	// These are distinct observations, each correctly reported once.
	if strings.Contains(err.Error(), "access denied") {
		err = waitEvent(t, errs)
	}
	if !strings.Contains(err.Error(), "file unavailable") {
		t.Fatalf("unexpected removal state: %v", err)
	}
	noEvent(t, errs, 70*time.Millisecond)
	noEvent(t, events, 40*time.Millisecond)
	writeFile(t, p, strings.Repeat("x", (1<<20)+1))
	_ = waitEvent(t, errs)
	noEvent(t, events, 40*time.Millisecond)
	writeFile(t, p, "recovered")
	waitEvent(t, events)
	cancel()
	if err := waitEvent(t, done); err != nil {
		t.Fatal(err)
	}
}
func TestWatchSeesChangeDuringCallbackAndContinuesAfterFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "first")
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	events := make(chan string, 10)
	errs := make(chan error, 10)
	var calls atomic.Int32
	cancel, done := startWatch(t, WatchOptions{Paths: []string{p}, Interval: 10 * time.Millisecond, Debounce: 20 * time.Millisecond, OnChange: func(context.Context) error {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		events <- string(b)
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-release
			return errors.New("secret callback")
		}
		return nil
	}, OnError: func(err error) { errs <- err }})
	waitEvent(t, entered)
	writeFile(t, p, "second")
	close(release)
	if got := waitEvent(t, events); got != "first" {
		t.Fatal(got)
	}
	if err := waitEvent(t, errs); strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	if got := waitEvent(t, events); got != "second" {
		t.Fatal(got)
	}
	cancel()
	if err := waitEvent(t, done); err != nil {
		t.Fatal(err)
	}
}
func TestWatchRejectsInvalidOptions(t *testing.T) {
	callback := func(context.Context) error { return nil }
	for _, o := range []WatchOptions{{}, {Paths: []string{"x"}}, {Paths: []string{""}, OnChange: callback}, {Paths: []string{"x", "./x"}, OnChange: callback}, {Paths: []string{"x"}, Interval: time.Millisecond, OnChange: callback}, {Paths: []string{"x"}, Debounce: -1, OnChange: callback}, {Paths: make([]string, 33), OnChange: callback}} {
		if err := Watch(context.Background(), o); err == nil {
			t.Fatal("accepted invalid options")
		}
	}
	if err := Watch(nil, WatchOptions{Paths: []string{"x"}, OnChange: callback}); err == nil { //nolint:staticcheck // 负向测试:显式验证 nil context 被拒绝
		t.Fatal("nil context accepted")
	}
}
func TestWatchRejectsDirectoryWithoutCallback(t *testing.T) {
	errs := make(chan error, 1)
	events := make(chan struct{}, 1)
	cancel, done := startWatch(t, WatchOptions{Paths: []string{t.TempDir()}, Interval: 10 * time.Millisecond, OnChange: func(context.Context) error { events <- struct{}{}; return nil }, OnError: func(err error) { errs <- err }})
	_ = waitEvent(t, errs)
	noEvent(t, events, 40*time.Millisecond)
	cancel()
	if err := waitEvent(t, done); err != nil {
		t.Fatal(err)
	}
}
func TestWatchFollowsSymlinkReplacement(t *testing.T) {
	d := t.TempDir()
	a, b, p := filepath.Join(d, "a"), filepath.Join(d, "b"), filepath.Join(d, "link")
	writeFile(t, a, "a")
	writeFile(t, b, "b")
	if err := os.Symlink(a, p); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	events := make(chan string, 4)
	cancel, done := startWatch(t, WatchOptions{Paths: []string{p}, Interval: 10 * time.Millisecond, Debounce: 20 * time.Millisecond, OnChange: func(context.Context) error { v, err := os.ReadFile(p); events <- string(v); return err }})
	if waitEvent(t, events) != "a" {
		t.Fatal("initial")
	}
	next := p + ".new"
	if err := os.Symlink(b, next); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, p); err != nil {
		t.Fatal(err)
	}
	if waitEvent(t, events) != "b" {
		t.Fatal("replacement")
	}
	cancel()
	if err := waitEvent(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestWatchFileHandlePermitsRemoval(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "old")
	f, err := openWatchFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
}

func TestWatchCanceledBeforeStartHasNoCallbacks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Watch(ctx, WatchOptions{Paths: []string{"missing"}, OnChange: func(context.Context) error { t.Fatal("callback after cancellation"); return nil }, OnError: func(error) { t.Fatal("error after cancellation") }})
	if err != nil {
		t.Fatal(err)
	}
}
