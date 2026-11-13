package memory_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/cache"
	"github.com/maxtanforge/go-frame/cache/memory"
)

func TestMemoryContract(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0)
	c, err := memory.New(memory.Config{Capacity: 2, Clock: func() time.Time { return now }, Options: cache.Options{MaxValueBytes: 8}})
	if err != nil {
		t.Fatal(err)
	}
	set := func(k, v string, ttl time.Duration) {
		t.Helper()
		if err := c.Set(ctx, k, []byte(v), ttl); err != nil {
			t.Fatal(err)
		}
	}
	hit := func(k, want string) {
		t.Helper()
		v, ok, e := c.Get(ctx, k)
		if e != nil || !ok || string(v) != want {
			t.Fatalf("%s got %q,%v,%v", k, v, ok, e)
		}
	}
	miss := func(k string) {
		t.Helper()
		_, ok, e := c.Get(ctx, k)
		if e != nil || ok {
			t.Fatalf("%s expected miss: %v %v", k, ok, e)
		}
	}
	set("a", "a", 0)
	set("b", "b", 0)
	hit("a", "a")
	set("c", "c", 0)
	miss("b")
	set("a", "new", time.Millisecond)
	hit("a", "new")
	now = now.Add(time.Millisecond)
	miss("a")
	set("c", "forever", 0)
	now = now.Add(time.Hour)
	hit("c", "forever")
}
func TestMemoryExpiryCopyBounds(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0)
	c, _ := memory.New(memory.Config{Capacity: 4, Clock: func() time.Time { return now }, Options: cache.Options{MaxValueBytes: 8}})
	b := []byte("value")
	if e := c.Set(ctx, "k", b, time.Millisecond); e != nil {
		t.Fatal(e)
	}
	b[0] = 'X'
	v, ok, e := c.Get(ctx, "k")
	if e != nil || !ok || string(v) != "value" {
		t.Fatal(v, ok, e)
	}
	v[0] = 'Y'
	v, _, _ = c.Get(ctx, "k")
	if string(v) != "value" {
		t.Fatal("output aliases storage")
	}
	if e := c.Set(ctx, "k", nil, 0); e != nil {
		t.Fatal(e)
	}
	now = now.Add(time.Hour)
	_, ok, e = c.Get(ctx, "k")
	if !ok || e != nil {
		t.Fatal("nil value must hit", e)
	}
	for _, v := range [][]byte{nil, {}} {
		if e := c.Set(ctx, "empty", v, 0); e != nil {
			t.Fatal(e)
		}
		b, ok, e := c.Get(ctx, "empty")
		if len(b) != 0 || !ok || e != nil {
			t.Fatal("empty hit", ok, e)
		}
	}
	for _, k := range []string{"", strings.Repeat("x", 513)} {
		if e := c.Set(ctx, k, nil, 0); !errors.Is(e, cache.ErrInvalidKey) {
			t.Fatal(e)
		}
		if _, _, e := c.Get(ctx, k); !errors.Is(e, cache.ErrInvalidKey) {
			t.Fatal(e)
		}
		if e := c.Delete(ctx, k); !errors.Is(e, cache.ErrInvalidKey) {
			t.Fatal(e)
		}
	}
	for _, ttl := range []time.Duration{-1, time.Nanosecond, time.Millisecond + 1} {
		if e := c.Set(ctx, "x", nil, ttl); !errors.Is(e, cache.ErrInvalidTTL) {
			t.Fatal(e)
		}
	}
	if e := c.Set(ctx, "x", make([]byte, 9), 0); !errors.Is(e, cache.ErrValueTooLarge) {
		t.Fatal(e)
	}
	if e := c.Set(ctx, strings.Repeat("x", 512), make([]byte, 8), 0); e != nil {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if e := c.Set(cancelled, "x", nil, 0); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, _, e := c.Get(cancelled, "x"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := c.Delete(cancelled, "x"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := c.Delete(ctx, "k"); e != nil {
		t.Fatal(e)
	}
	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Fatal("delete")
	}
	for i := 0; i < 2; i++ {
		if e := c.Close(); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, e := c.Get(ctx, "x"); !errors.Is(e, cache.ErrClosed) {
		t.Fatal(e)
	}
	if e := c.Set(ctx, "x", nil, 0); !errors.Is(e, cache.ErrClosed) {
		t.Fatal(e)
	}
	if e := c.Delete(ctx, "x"); !errors.Is(e, cache.ErrClosed) {
		t.Fatal(e)
	}
}
func TestMemoryConfigAndIndependence(t *testing.T) {
	for _, cfg := range []memory.Config{{}, {Capacity: -1}, {Capacity: 1, Options: cache.Options{MaxValueBytes: -1}}} {
		if _, e := memory.New(cfg); e == nil {
			t.Fatal("invalid config accepted")
		}
	}
	a, _ := memory.New(memory.Config{Capacity: 2})
	b, _ := memory.New(memory.Config{Capacity: 2})
	ctx := context.Background()
	_ = a.Set(ctx, "x", []byte("a"), 0)
	if _, ok, _ := b.Get(ctx, "x"); ok {
		t.Fatal("shared state")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = a.Set(ctx, "x", nil, 0)
				_, _, _ = a.Get(ctx, "x")
				_ = a.Delete(ctx, "x")
			}
		}()
	}
	wg.Wait()
}
