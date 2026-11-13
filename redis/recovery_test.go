package redis

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/internal/faulttest"
)

func TestRecoveryIntegration(t *testing.T) {
	if os.Getenv("FRAME_TEST_RECOVERY") != "1" {
		t.Skip("set FRAME_TEST_RECOVERY=1 to opt in")
	}
	addr := os.Getenv("FRAME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Fatal("Redis test address required")
	}
	db := 0
	if raw := os.Getenv("FRAME_TEST_REDIS_DB"); raw != "" {
		var err error
		db, err = strconv.Atoi(raw)
		if err != nil || db < 0 {
			t.Fatal("invalid Redis test DB")
		}
	}
	p := faulttest.NewProxy(t, addr)
	c, err := Open(Options{Addr: p.Addr(), Username: os.Getenv("FRAME_TEST_REDIS_USERNAME"), Password: os.Getenv("FRAME_TEST_REDIS_PASSWORD"), DB: db, PoolSize: 1, DialTimeout: 500 * time.Millisecond, ReadTimeout: 500 * time.Millisecond, WriteTimeout: 500 * time.Millisecond, PoolTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal("cannot construct Redis test client")
	}
	t.Cleanup(func() {
		if c.Close() != nil {
			t.Error("Redis cleanup failed")
		}
	})
	ping := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return c.Ping(ctx)
	}
	if ping() != nil {
		t.Fatal("initial Redis connectivity failed")
	}
	held := c.Client.Conn()
	defer func() { _ = held.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = held.Ping(ctx).Err()
	cancel()
	if err != nil {
		t.Fatal("cannot hold Redis pool connection")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = c.Ping(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Redis pool exhaustion did not honor deadline")
	}
	if c.Client.PoolStats().TotalConns > 1 {
		t.Fatal("Redis pool exceeded connection capacity")
	}
	_ = held.Close()
	p.Block(true)
	ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
	start := time.Now()
	err = c.Ping(ctx)
	cancel()
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatal("disconnected Redis request did not fail within budget")
	}
	p.Block(false)
	if ping() != nil {
		t.Fatal("same Redis client did not recover")
	}
}
