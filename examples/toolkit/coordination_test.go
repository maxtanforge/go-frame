package toolkit

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	lockredis "github.com/maxtanforge/go-frame/lock/redis"
	redis "github.com/redis/go-redis/v9"
)

// Exercise the real lease lifecycle without depending on a Redis service.
type leaseClient struct {
	releases   atomic.Int32
	releaseErr error
	cleanupErr error
}

func (c *leaseClient) Do(ctx context.Context, _ ...interface{}) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	cmd.SetVal("OK")
	return cmd
}

func (c *leaseClient) Eval(ctx context.Context, _ string, _ []string, args ...interface{}) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	if len(args) == 1 {
		c.releases.Add(1)
		c.cleanupErr = ctx.Err()
		if _, ok := ctx.Deadline(); !ok {
			c.cleanupErr = errors.New("release has no deadline")
		}
		cmd.SetErr(c.releaseErr)
	}
	cmd.SetVal(int64(1))
	return cmd
}

func testLeaseLocker(t *testing.T, client *leaseClient) *lockredis.Locker {
	t.Helper()
	locker, err := lockredis.New(client, lockredis.Config{Namespace: "test", TTLMS: 2000, CommandTimeoutMS: 100, PollIntervalMS: 1, RenewIntervalMS: 10})
	if err != nil {
		t.Fatal(err)
	}
	return locker
}

func TestWithLeaseReleasesOnAbnormalExit(t *testing.T) {
	for _, exit := range []string{"panic", "goexit"} {
		t.Run(exit, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel() // Stop lease watchers even when the regression fails.
			client := new(leaseClient)
			locker := testLeaseLocker(t, client)
			done := make(chan struct{})
			workContext := make(chan context.Context, 1)
			panicValue := make(chan any, 1)
			returned := make(chan struct{}, 1)
			go func() {
				defer close(done)
				defer func() { panicValue <- recover() }()
				_ = WithLease(ctx, locker, "key", func(workCtx context.Context) error {
					workContext <- workCtx
					if exit == "panic" {
						panic("work panic")
					}
					runtime.Goexit()
					return nil
				})
				returned <- struct{}{}
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("WithLease did not finish abnormal exit")
			}
			var workCtx context.Context
			select {
			case workCtx = <-workContext:
			default:
				t.Fatal("lease work was not called")
			}
			if client.releases.Load() != 1 {
				t.Fatal("abnormal exit did not release the lease")
			}
			if client.cleanupErr != nil {
				t.Fatalf("invalid cleanup context: %v", client.cleanupErr)
			}
			if !errors.Is(context.Cause(workCtx), lockredis.ErrReleased) {
				t.Fatalf("lease work context was not released: %v", context.Cause(workCtx))
			}
			got := <-panicValue
			if exit == "panic" && got != "work panic" || exit == "goexit" && got != nil {
				t.Fatalf("unexpected panic propagation: %v", got)
			}
			select {
			case <-returned:
				t.Fatal("abnormal exit returned normally")
			default:
			}
		})
	}
}

func TestWithLeaseJoinsWorkAndReleaseErrorsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workErr, releaseErr := errors.New("work failed"), errors.New("release failed")
	client := &leaseClient{releaseErr: releaseErr}
	err := WithLease(ctx, testLeaseLocker(t, client), "key", func(context.Context) error {
		cancel()
		return workErr
	})
	if !errors.Is(err, workErr) || !errors.Is(err, releaseErr) {
		t.Fatalf("missing work or release cause: %v", err)
	}
	if client.releases.Load() != 1 || client.cleanupErr != nil {
		t.Fatalf("release failed to use an independent bounded context: count=%d, error=%v", client.releases.Load(), client.cleanupErr)
	}
}
