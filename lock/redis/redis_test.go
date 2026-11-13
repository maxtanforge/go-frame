package redis

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	driver "github.com/redis/go-redis/v9"
)

type fakeClient struct {
	mu           sync.Mutex
	owner        string
	key          string
	ttl          int64
	releaseCalls int
	renewCalls   int
	renewErr     error
	blockRenew   bool
	acquireErr   error
	expired      bool
}

// delayedCancelClient models socket I/O that honors its original deadline but
// does not wake immediately when the parent context is canceled.
type delayedCancelClient struct {
	fakeClient
	started  chan struct{}
	finished chan struct{}
	deleted  chan struct{}
	badOrder bool
}

func (f *delayedCancelClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *driver.Cmd {
	if script == renewScript {
		close(f.started)
		deadline, _ := ctx.Deadline()
		time.Sleep(time.Until(deadline))
		close(f.finished)
		return result(nil, ctx.Err())
	}
	select {
	case <-f.finished:
	default:
		f.badOrder = true
	}
	close(f.deleted)
	return f.fakeClient.Eval(ctx, script, keys, args...)
}

func TestReleaseDeadlineDuringDelayedRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &delayedCancelClient{started: make(chan struct{}), finished: make(chan struct{}), deleted: make(chan struct{})}
		cfg := config()
		cfg.TTLMS = 1000
		cfg.CommandTimeoutMS = 200
		cfg.RenewIntervalMS = 10
		locks, err := New(f, cfg)
		if err != nil {
			t.Fatal(err)
		}
		held, err := locks.Acquire(context.Background(), "a", false)
		if err != nil {
			t.Fatal(err)
		}
		<-f.started
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err = held.Release(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("release: %v", err)
		}
		if elapsed := time.Since(start); elapsed != 5*time.Millisecond {
			t.Errorf("release exceeded caller budget: %v", elapsed)
		}
		<-held.releaseDone
		select {
		case <-f.finished:
		default:
			t.Fatal("cleanup ended before renewal")
		}
		select {
		case <-f.deleted:
		default:
			t.Fatal("cleanup skipped deletion after caller deadline")
		}
		if f.badOrder || f.releaseCalls != 1 {
			t.Fatal("cleanup overlapped renewal or deleted more than once")
		}
		if err = held.Release(context.Background()); err != nil {
			t.Fatalf("cleanup result: %v", err)
		}
	})
}

func TestReleaseCanceledCallerStillCleansUp(t *testing.T) {
	f := &fakeClient{}
	locks, err := New(f, config())
	if err != nil {
		t.Fatal(err)
	}
	held, err := locks.Acquire(context.Background(), "a", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = held.Release(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = held.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owner != "" || f.releaseCalls != 1 {
		t.Fatal("canceled caller prevented owner-checked deletion")
	}
}

func TestConcurrentReleaseWaitUsesEachCallerContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &delayedCancelClient{started: make(chan struct{}), finished: make(chan struct{}), deleted: make(chan struct{})}
		cfg := config()
		cfg.TTLMS = 1000
		cfg.CommandTimeoutMS = 200
		cfg.RenewIntervalMS = 10
		locks, err := New(f, cfg)
		if err != nil {
			t.Fatal(err)
		}
		held, err := locks.Acquire(context.Background(), "a", false)
		if err != nil {
			t.Fatal(err)
		}
		<-f.started
		first := make(chan error, 1)
		go func() { first <- held.Release(context.Background()) }()
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err = held.Release(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("second caller: %v", err)
		}
		if elapsed := time.Since(start); elapsed != 5*time.Millisecond {
			t.Errorf("second caller exceeded budget: %v", elapsed)
		}
		if err = <-first; err != nil {
			t.Fatal(err)
		}
		if f.badOrder {
			t.Fatal("delete overlapped renewal")
		}
		if f.releaseCalls != 1 {
			t.Fatalf("deletion count: %d", f.releaseCalls)
		}
		if err = held.Release(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func result(v interface{}, err error) *driver.Cmd {
	c := driver.NewCmd(context.Background())
	c.SetVal(v)
	c.SetErr(err)
	return c
}
func (f *fakeClient) Do(ctx context.Context, args ...interface{}) *driver.Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		return result(nil, errors.New("missing command deadline"))
	}
	if f.acquireErr != nil {
		return result(nil, f.acquireErr)
	}
	if f.owner != "" {
		return result(nil, driver.Nil)
	}
	if len(args) != 6 || args[0] != "SET" || args[3] != "NX" || args[4] != "PX" {
		return result(nil, errors.New("not SET NX PX"))
	}
	f.key = args[1].(string)
	f.owner = args[2].(string)
	f.ttl = args[5].(int64)
	return result("OK", nil)
}
func (f *fakeClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *driver.Cmd {
	if _, ok := ctx.Deadline(); !ok {
		return result(nil, errors.New("missing command deadline"))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if script == renewScript {
		f.renewCalls++
		if f.blockRenew {
			f.mu.Unlock()
			<-ctx.Done()
			f.mu.Lock()
			return result(nil, ctx.Err())
		}
		if f.renewErr != nil {
			return result(nil, f.renewErr)
		}
	} else if script == releaseScript {
		f.releaseCalls++
	} else {
		return result(nil, errors.New("unknown script"))
	}
	if keys[0] != f.key || args[0] != f.owner || f.expired {
		return result(int64(0), nil)
	}
	if script == releaseScript {
		f.owner = ""
	}
	return result(int64(1), nil)
}
func config() Config {
	return Config{Namespace: "jobs", TTLMS: 150, CommandTimeoutMS: 30, PollIntervalMS: 5}
}
func locker(t *testing.T, f *fakeClient, c Config) *Locker {
	t.Helper()
	l, e := New(f, c)
	if e != nil {
		t.Fatal(e)
	}
	return l
}
func canceled(t *testing.T, l *Lease) {
	t.Helper()
	select {
	case <-l.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("lease context never canceled")
	}
}

func TestAcquireAndOwnerCheckedConcurrentRelease(t *testing.T) {
	f := &fakeClient{}
	l := locker(t, f, config())
	lease, e := l.Acquire(context.Background(), "a", false)
	if e != nil {
		t.Fatal(e)
	}
	if f.key != "jobs:a" || len(f.owner) < 32 || f.ttl != 150 {
		t.Fatalf("bad SET: %+v", f)
	}
	if _, e = l.Acquire(context.Background(), "a", false); !errors.Is(e, ErrContended) {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.owner = "new owner"
	f.mu.Unlock()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if e := lease.Release(context.Background()); !errors.Is(e, ErrLost) {
				t.Errorf("release=%v", e)
			}
		})
	}
	wg.Wait()
	if f.releaseCalls != 1 || f.owner != "new owner" {
		t.Fatalf("unsafe release %+v", f)
	}
	canceled(t, lease)
}
func TestExpiryCancelsWork(t *testing.T) {
	c := config()
	c.TTLMS = 30
	l, e := locker(t, &fakeClient{}, c).Acquire(context.Background(), "a", false)
	if e != nil {
		t.Fatal(e)
	}
	canceled(t, l)
	if !errors.Is(l.Err(), ErrLost) {
		t.Fatal(l.Err())
	}
	_ = l.Release(context.Background())
}
func TestRenewalErrorAndTimeoutLoseLease(t *testing.T) {
	for _, block := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "timeout"}[block], func(t *testing.T) {
			f := &fakeClient{renewErr: errors.New("disconnected"), blockRenew: block}
			c := config()
			c.RenewIntervalMS = 10
			l, e := locker(t, f, c).Acquire(context.Background(), "a", false)
			if e != nil {
				t.Fatal(e)
			}
			canceled(t, l)
			if !errors.Is(context.Cause(l.Context()), ErrLost) {
				t.Fatal(l.Err())
			}
			_ = l.Release(context.Background())
		})
	}
}
func TestRenewalKeepsLeaseAndReleaseStopsIt(t *testing.T) {
	f := &fakeClient{}
	c := config()
	c.TTLMS = 60
	c.RenewIntervalMS = 10
	l, e := locker(t, f, c).Acquire(context.Background(), "a", false)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-l.Context().Done():
		t.Fatal(l.Err())
	case <-time.After(150 * time.Millisecond):
	}
	if e = l.Release(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	n := f.renewCalls
	f.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renewCalls != n || n == 0 {
		t.Fatal("renewal cleanup failed")
	}
}
func TestWaitingAndParentCancellation(t *testing.T) {
	f := &fakeClient{owner: "other"}
	l := locker(t, f, config())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e := l.Acquire(ctx, "a", true); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.owner = ""
	f.mu.Unlock()
	parent, stop := context.WithCancel(context.Background())
	lease, e := l.Acquire(parent, "a", false)
	if e != nil {
		t.Fatal(e)
	}
	stop()
	canceled(t, lease)
	if !errors.Is(lease.Err(), context.Canceled) {
		t.Fatal(lease.Err())
	}
	_ = lease.Release(context.Background())
}
func TestUnknownAcquireOutcomeIsError(t *testing.T) {
	sentinel := errors.New("network")
	l, e := locker(t, &fakeClient{acquireErr: sentinel}, config()).Acquire(context.Background(), "a", true)
	if l != nil || !errors.Is(e, sentinel) {
		t.Fatalf("%v %v", l, e)
	}
}
func TestConfigValidation(t *testing.T) {
	for _, change := range []func(*Config){func(c *Config) { c.Namespace = "" }, func(c *Config) { c.TTLMS = 0 }, func(c *Config) { c.TTLMS = math.MaxInt64 }, func(c *Config) { c.CommandTimeoutMS = -1 }, func(c *Config) { c.PollIntervalMS = 0 }, func(c *Config) { c.RenewIntervalMS = c.TTLMS }, func(c *Config) { c.RenewIntervalMS = -1 }} {
		c := config()
		change(&c)
		if _, e := New(&fakeClient{}, c); e == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	var f *fakeClient
	if _, e := New(f, config()); e == nil {
		t.Fatal("typed nil accepted")
	}
}

func TestScriptsCompareOwnerBeforeMutation(t *testing.T) {
	for name, script := range map[string]string{"renew": renewScript, "release": releaseScript} {
		if !strings.Contains(script, "redis.call('GET', KEYS[1]) == ARGV[1]") {
			t.Fatalf("%s does not compare owner", name)
		}
	}
	if !strings.Contains(renewScript, "redis.call('PEXPIRE', KEYS[1], ARGV[2])") || !strings.Contains(releaseScript, "redis.call('DEL', KEYS[1])") {
		t.Fatal("missing atomic mutation")
	}
}

func TestOwnerReplacementCancelsRenewal(t *testing.T) {
	f := &fakeClient{}
	c := config()
	c.RenewIntervalMS = 10
	held, err := locker(t, f, c).Acquire(context.Background(), "a", false)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.owner = "replacement"
	f.mu.Unlock()
	canceled(t, held)
	if !errors.Is(held.Err(), ErrLost) {
		t.Fatal(held.Err())
	}
	if err = held.Release(context.Background()); !errors.Is(err, ErrLost) {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owner != "replacement" {
		t.Fatal("deleted replacement")
	}
}

func TestWaitingAcquiresAfterRelease(t *testing.T) {
	f := &fakeClient{}
	locks := locker(t, f, config())
	held, err := locks.Acquire(context.Background(), "a", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	acquired := make(chan *Lease, 1)
	failed := make(chan error, 1)
	go func() {
		next, e := locks.Acquire(ctx, "a", true)
		if e != nil {
			failed <- e
			return
		}
		acquired <- next
	}()
	if err = held.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case next := <-acquired:
		if e := next.Release(context.Background()); e != nil {
			t.Fatal(e)
		}
	case e := <-failed:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("waiter never acquired")
	}
}
