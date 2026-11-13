package redis_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/cache"
	adapter "github.com/maxtanforge/go-frame/cache/redis"
	connection "github.com/maxtanforge/go-frame/redis"
)

func TestRedisCacheIntegration(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := connection.OpenReady(ctx, connection.Options{Addr: addr, Username: os.Getenv("FRAME_TEST_REDIS_USERNAME"), Password: os.Getenv("FRAME_TEST_REDIS_PASSWORD"), DB: db})
	if err != nil {
		t.Fatal("Redis cache connection failed")
	}
	namespace := "frame-cache-integration:" + rand.Text()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := pool.Client.Del(cleanup, namespace+":value", namespace+":large", namespace+":expires").Err(); err != nil {
			t.Error("Redis cache fixture cleanup failed")
		}
		if err := pool.Close(); err != nil {
			t.Error("Redis cache connection close failed")
		}
	})
	store, err := adapter.New(pool.Client, adapter.Config{Namespace: namespace, Options: cache.Options{MaxValueBytes: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(ctx, "value"); err != nil || found {
		t.Fatal("missing cache value was not a miss")
	}
	for _, value := range [][]byte{{}, {0, 1, 2, 255}} {
		if err := store.Set(ctx, "value", value, time.Minute); err != nil {
			t.Fatal("cache write failed")
		}
		got, found, err := store.Get(ctx, "value")
		if err != nil || !found || string(got) != string(value) {
			t.Fatal("empty/binary cache value round trip failed")
		}
	}
	if err := pool.Client.Set(ctx, namespace+":large", "12345", time.Minute).Err(); err != nil {
		t.Fatal("oversized fixture write failed")
	}
	if value, found, err := store.Get(ctx, "large"); !errors.Is(err, cache.ErrValueTooLarge) || !found || value != nil {
		t.Fatal("Lua read did not reject an existing oversized value")
	}
	if err := store.Set(ctx, "expires", []byte("ttl"), 100*time.Millisecond); err != nil {
		t.Fatal("expiring cache write failed")
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, found, err := store.Get(ctx, "expires")
		if err != nil {
			t.Fatal("expiring cache read failed")
		}
		if !found {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("cache TTL did not expire")
		case <-ticker.C:
		}
	}
	if err := store.Delete(ctx, "value"); err != nil {
		t.Fatal("cache delete failed")
	}
	if _, found, err := store.Get(ctx, "value"); err != nil || found {
		t.Fatal("deleted cache value remains visible")
	}
}
