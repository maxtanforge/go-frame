// Package redis provides cooperative leases using a borrowed Redis client.
// It does not provide fencing or exactly-once execution.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/maxtanforge/go-frame/internal/isnil"
	driver "github.com/redis/go-redis/v9"
)

var (
	// ErrContended reports a busy lease: another owner holds or once held the key.
	ErrContended = errors.New("lock/redis: lease held by another owner")
	// ErrLost ends the lease context when ownership is gone.
	ErrLost = errors.New("lock/redis: lease ownership lost")
	// ErrReleased ends the lease context when the owner releases it.
	ErrReleased = errors.New("lock/redis: lease released")
)

// Client is implemented by go-redis Client and ClusterClient. Implementations
// must honor context deadlines; configure go-redis ContextTimeoutEnabled and
// bounded transport timeouts/retries. This package never closes the client.
type Client interface {
	Do(context.Context, ...interface{}) *driver.Cmd
	Eval(context.Context, string, []string, ...interface{}) *driver.Cmd
}

// Config uses integer milliseconds. All durations must be positive except
// RenewIntervalMS, where zero disables automatic renewal.
type Config struct {
	Namespace        string
	TTLMS            int64
	CommandTimeoutMS int64
	PollIntervalMS   int64
	RenewIntervalMS  int64
}

// Locker grants expiring leases through compare-and-swap Lua scripts.
type Locker struct {
	client                           Client
	cfg                              Config
	ttl, commandTimeout, poll, renew time.Duration
}

// New validates the configuration and borrows the client without connecting.
func New(client Client, cfg Config) (*Locker, error) {
	if isnil.Check(client) {
		return nil, errors.New("lock/redis: nil client")
	}
	if !validKey(cfg.Namespace) {
		return nil, errors.New("lock/redis: invalid namespace")
	}
	for name, ms := range map[string]int64{"TTLMS": cfg.TTLMS, "CommandTimeoutMS": cfg.CommandTimeoutMS, "PollIntervalMS": cfg.PollIntervalMS, "RenewIntervalMS": cfg.RenewIntervalMS} {
		if ms < 0 || (ms == 0 && name != "RenewIntervalMS") || ms > math.MaxInt64/int64(time.Millisecond) {
			return nil, fmt.Errorf("lock/redis: invalid %s", name)
		}
	}
	if cfg.RenewIntervalMS >= cfg.TTLMS {
		return nil, errors.New("lock/redis: renewal interval must be less than TTL")
	}
	return &Locker{client: client, cfg: cfg, ttl: time.Duration(cfg.TTLMS) * time.Millisecond, commandTimeout: time.Duration(cfg.CommandTimeoutMS) * time.Millisecond, poll: time.Duration(cfg.PollIntervalMS) * time.Millisecond, renew: time.Duration(cfg.RenewIntervalMS) * time.Millisecond}, nil
}

func validKey(s string) bool { return s != "" && len(s) <= 512 && strings.TrimSpace(s) == s }

const renewScript = `if redis.call('GET', KEYS[1]) == ARGV[1] then
 return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`
const releaseScript = `if redis.call('GET', KEYS[1]) == ARGV[1] then
 return redis.call('DEL', KEYS[1])
end
return 0`

// Acquire tries once unless wait is true. Waiting polls until acquisition or
// cancellation; callers wanting a finite total wait must set a context deadline.
// Any uncertain command outcome returns an error; the possible orphan expires.
func (l *Locker) Acquire(ctx context.Context, key string, wait bool) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validKey(key) {
		return nil, errors.New("lock/redis: invalid key")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("lock/redis: owner: %w", err)
	}
	owner := hex.EncodeToString(random[:])
	key = l.cfg.Namespace + ":" + key
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start := time.Now()
		commandCtx, cancel := context.WithTimeout(ctx, min(l.commandTimeout, l.ttl))
		value, err := l.client.Do(commandCtx, "SET", key, owner, "NX", "PX", l.cfg.TTLMS).Result()
		contextErr := commandCtx.Err()
		cancel()
		if contextErr != nil {
			return nil, contextErr
		}
		if err == nil {
			if value != "OK" {
				return nil, fmt.Errorf("%w: unexpected SET response", ErrLost)
			}
			deadline := start.Add(l.ttl)
			if !time.Now().Before(deadline) {
				return nil, ErrLost
			}
			workCtx, workCancel := context.WithCancelCause(ctx)
			lease := &Lease{locker: l, key: key, owner: owner, ctx: workCtx, cancel: workCancel, deadline: deadline, changed: make(chan struct{}, 1), releaseDone: make(chan struct{})}
			lease.wg.Go(lease.watch)
			if l.renew > 0 {
				lease.wg.Go(lease.renewLoop)
			}
			return lease, nil
		}
		if !errors.Is(err, driver.Nil) {
			return nil, fmt.Errorf("lock/redis: acquire outcome unknown: %w", err)
		}
		if !wait {
			return nil, ErrContended
		}
		timer := time.NewTimer(l.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Lease owns a cooperative work context. A canceled context must stop business
// work; Redis failover and process pauses still require business-side fencing.
type Lease struct {
	locker      *Locker
	key, owner  string
	ctx         context.Context
	cancel      context.CancelCauseFunc
	mu          sync.Mutex
	deadline    time.Time
	changed     chan struct{}
	wg          sync.WaitGroup
	releaseOnce sync.Once
	releaseDone chan struct{}
	releaseErr  error
}

// Context ends when the lease is released, lost or the parent is canceled.
func (l *Lease) Context() context.Context { return l.ctx }

// Err reports the cancellation cause (including ErrLost), or nil while active.
func (l *Lease) Err() error { return context.Cause(l.ctx) }

func (l *Lease) watch() {
	for {
		l.mu.Lock()
		remaining := time.Until(l.deadline)
		l.mu.Unlock()
		timer := time.NewTimer(max(remaining, 0))
		select {
		case <-l.ctx.Done():
			timer.Stop()
			return
		case <-l.changed:
			timer.Stop()
		case <-timer.C:
			l.mu.Lock()
			if !time.Now().Before(l.deadline) {
				l.cancel(ErrLost)
				l.mu.Unlock()
				return
			}
			l.mu.Unlock()
		}
	}
}

func (l *Lease) renewLoop() {
	ticker := time.NewTicker(l.locker.renew)
	defer ticker.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
		}
		if l.ctx.Err() != nil {
			return
		}
		l.mu.Lock()
		deadline := l.deadline
		l.mu.Unlock()
		start := time.Now()
		commandCtx, cancel := context.WithDeadline(l.ctx, minTime(deadline, start.Add(l.locker.commandTimeout)))
		value, err := l.locker.client.Eval(commandCtx, renewScript, []string{l.key}, l.owner, l.locker.cfg.TTLMS).Int64()
		contextErr := commandCtx.Err()
		cancel()
		l.mu.Lock()
		if l.ctx.Err() != nil {
			l.mu.Unlock()
			return
		}
		if err != nil || contextErr != nil || value != 1 || !time.Now().Before(deadline) {
			cause := ErrLost
			if err != nil {
				cause = fmt.Errorf("%w: renewal: %w", ErrLost, err)
			} else if contextErr != nil {
				cause = fmt.Errorf("%w: renewal: %w", ErrLost, contextErr)
			}
			l.cancel(cause)
			l.mu.Unlock()
			return
		}
		l.deadline = start.Add(l.locker.ttl)
		l.mu.Unlock()
		select {
		case l.changed <- struct{}{}:
		default:
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Release cancels work, stops renewal, and compares the owner before deletion.
// It is safe to call concurrently. Each caller's context bounds its wait. The
// cleanup goroutine joins any in-flight renewal before deleting with a fresh
// command timeout independent of caller cancellation. Cleanup can outlive the
// wait by the renewal deadline plus deletion budget. Later calls await its result.
// A failed deletion may leave a key until TTL; no further renewal is performed.
func (l *Lease) Release(ctx context.Context) error {
	l.releaseOnce.Do(func() {
		l.cancel(ErrReleased)
		go l.cleanup(ctx)
	})
	select {
	case <-l.releaseDone:
		return l.releaseErr
	default:
	}
	select {
	case <-l.releaseDone:
		return l.releaseErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Lease) cleanup(ctx context.Context) {
	defer close(l.releaseDone)
	l.wg.Wait()
	commandCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.locker.commandTimeout)
	defer cancel()
	if err := commandCtx.Err(); err != nil {
		l.releaseErr = err
		return
	}
	value, err := l.locker.client.Eval(commandCtx, releaseScript, []string{l.key}, l.owner).Int64()
	if contextErr := commandCtx.Err(); contextErr != nil {
		l.releaseErr = contextErr
		return
	}
	if err != nil {
		l.releaseErr = fmt.Errorf("lock/redis: release outcome unknown: %w", err)
		return
	}
	if value != 1 {
		l.releaseErr = ErrLost
	}
}
