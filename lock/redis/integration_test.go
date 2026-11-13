package redis

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	connection "github.com/maxtanforge/go-frame/redis"
)

func TestRedisLockIntegration(t *testing.T) {
	addr := os.Getenv("FRAME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set FRAME_TEST_REDIS_ADDR to opt in")
	}
	db := 0
	if raw := os.Getenv("FRAME_TEST_REDIS_DB"); raw != "" {
		var err error
		db, err = strconv.Atoi(raw)
		if err != nil || db < 0 {
			t.Fatal("invalid FRAME_TEST_REDIS_DB")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := connection.OpenReady(ctx, connection.Options{Addr: addr, Username: os.Getenv("FRAME_TEST_REDIS_USERNAME"), Password: os.Getenv("FRAME_TEST_REDIS_PASSWORD"), DB: db})
	if err != nil {
		t.Fatal("Redis lock connection failed")
	}
	namespace := "frame-lock-integration:" + rand.Text()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := pool.Client.Del(cleanup, namespace+":renew", namespace+":replace", namespace+":expiry").Err(); err != nil {
			t.Error("Redis lock fixture cleanup failed")
		}
		if err := pool.Close(); err != nil {
			t.Error("Redis lock connection close failed")
		}
	})
	newLocker := func(renew int64) *Locker {
		t.Helper()
		locker, err := New(pool.Client, Config{Namespace: namespace, TTLMS: 2000, CommandTimeoutMS: 750, PollIntervalMS: 50, RenewIntervalMS: renew})
		if err != nil {
			t.Fatal(err)
		}
		return locker
	}
	acquire := func(locker *Locker, key string) *Lease {
		t.Helper()
		lease, err := locker.Acquire(ctx, key, false)
		if err != nil {
			t.Fatal("lease acquisition failed")
		}
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			if err := lease.Release(cleanup); err != nil && !errors.Is(err, ErrLost) {
				t.Error("lease cleanup failed")
			}
		})
		return lease
	}
	owner, contender := newLocker(400), newLocker(0)
	lease := acquire(owner, "renew")
	if _, err := contender.Acquire(ctx, "renew", false); !errors.Is(err, ErrContended) {
		t.Fatal("concurrent owner acquired held lease")
	}
	waitCtx, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = contender.Acquire(waitCtx, "renew", true)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("contended wait did not honor deadline")
	}
	select {
	case <-lease.Context().Done():
		t.Fatal("renewing lease ended before original TTL")
	case <-time.After(2500 * time.Millisecond):
	}
	if _, err := contender.Acquire(ctx, "renew", false); !errors.Is(err, ErrContended) {
		t.Fatal("renewal did not preserve exclusivity beyond original TTL")
	}
	if err := lease.Release(ctx); err != nil || !errors.Is(lease.Err(), ErrReleased) {
		t.Fatal("lease release failed")
	}
	replacement := acquire(contender, "renew")
	if err := replacement.Release(ctx); err != nil {
		t.Fatal("replacement lease release failed")
	}
	old := acquire(contender, "replace")
	if err := pool.Client.Set(ctx, namespace+":replace", "replacement-owner", time.Minute).Err(); err != nil {
		t.Fatal("replacement owner fixture failed")
	}
	if err := old.Release(ctx); !errors.Is(err, ErrLost) {
		t.Fatal("old owner did not detect lost ownership")
	}
	if value, err := pool.Client.Get(ctx, namespace+":replace").Result(); err != nil || value != "replacement-owner" {
		t.Fatal("old owner deleted replacement lock")
	}
	expiring := acquire(contender, "expiry")
	select {
	case <-expiring.Context().Done():
		if !errors.Is(expiring.Err(), ErrLost) {
			t.Fatal("expired lease did not report ownership loss")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("unrenewed lease did not expire")
	}
	// The local conservative deadline precedes Redis expiry by network latency.
	waitCtx, stop = context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	next, err := contender.Acquire(waitCtx, "expiry", true)
	if err != nil {
		t.Fatal("expired Redis lease could not be reacquired")
	}
	if err := next.Release(ctx); err != nil {
		t.Fatal("reacquired lease release failed")
	}
}
