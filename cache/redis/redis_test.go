package redis_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/cache"
	adapter "github.com/maxtanforge/go-frame/cache/redis"
	driver "github.com/redis/go-redis/v9"
)

type fake struct {
	result interface{}
	err    error
	script string
	keys   []string
	args   []interface{}
	ttl    time.Duration
	calls  int
}

func (f *fake) Eval(_ context.Context, s string, k []string, a ...interface{}) *driver.Cmd {
	f.calls++
	f.script = s
	f.keys = k
	f.args = a
	return driver.NewCmdResult(f.result, f.err)
}
func (f *fake) Set(_ context.Context, k string, v interface{}, ttl time.Duration) *driver.StatusCmd {
	f.calls++
	f.keys = []string{k}
	f.args = []interface{}{v}
	f.ttl = ttl
	return driver.NewStatusResult("OK", f.err)
}
func (f *fake) Del(_ context.Context, k ...string) *driver.IntCmd {
	f.calls++
	f.keys = k
	return driver.NewIntResult(1, f.err)
}
func TestRedisCommands(t *testing.T) {
	ctx := context.Background()
	f := &fake{result: "hello"}
	c, e := adapter.New(f, adapter.Config{Namespace: "app", Options: cache.Options{MaxValueBytes: 8}})
	if e != nil {
		t.Fatal(e)
	}
	v, ok, e := c.Get(ctx, "key")
	if string(v) != "hello" || !ok || e != nil {
		t.Fatal(v, ok, e)
	}
	if len(f.keys) != 1 || f.keys[0] != "app:key" || len(f.args) != 1 || f.args[0] != 8 {
		t.Fatal(f.keys, f.args)
	}
	length := strings.Index(f.script, "STRLEN")
	get := strings.Index(f.script, "'GET'")
	if length < 0 || get < length {
		t.Fatal("must bound value before GET", f.script)
	}
	for _, ttl := range []time.Duration{0, time.Millisecond} {
		if e = c.Set(ctx, "key", nil, ttl); e != nil || f.ttl != ttl || f.keys[0] != "app:key" {
			t.Fatal(e, f.ttl)
		}
	}
	if e = c.Delete(ctx, "key"); e != nil || f.keys[0] != "app:key" {
		t.Fatal(e)
	}
	f.err = driver.Nil
	if _, ok, e = c.Get(ctx, "key"); ok || e != nil {
		t.Fatal(ok, e)
	}
	for _, backend := range []error{errors.New("WRONGTYPE"), errors.New("offline"), context.Canceled} {
		f.err = backend
		if _, _, e = c.Get(ctx, "key"); !errors.Is(e, backend) {
			t.Fatal(e)
		}
		if e = c.Set(ctx, "key", nil, 0); !errors.Is(e, backend) {
			t.Fatal(e)
		}
		if e = c.Delete(ctx, "key"); !errors.Is(e, backend) {
			t.Fatal(e)
		}
	}
	f.err = nil
	f.result = int64(0)
	if _, ok, e = c.Get(ctx, "key"); !ok || !errors.Is(e, cache.ErrValueTooLarge) {
		t.Fatal(ok, e)
	}
	f.result = ""
	if v, ok, e = c.Get(ctx, "key"); len(v) != 0 || !ok || e != nil {
		t.Fatal(v, ok, e)
	}
	before := f.calls
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, e = c.Get(cancelled, "key"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = c.Set(cancelled, "key", nil, 0); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = c.Delete(cancelled, "key"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = c.Set(ctx, "key", make([]byte, 9), 0); !errors.Is(e, cache.ErrValueTooLarge) {
		t.Fatal(e)
	}
	if e = c.Set(ctx, "key", nil, time.Nanosecond); !errors.Is(e, cache.ErrInvalidTTL) {
		t.Fatal(e)
	}
	if _, _, e = c.Get(ctx, ""); !errors.Is(e, cache.ErrInvalidKey) {
		t.Fatal(e)
	}
	if e = c.Delete(ctx, strings.Repeat("x", 513)); !errors.Is(e, cache.ErrInvalidKey) {
		t.Fatal(e)
	}
	if f.calls != before {
		t.Fatal("invalid operations reached backend")
	}
}
func TestRedisConfig(t *testing.T) {
	if _, e := adapter.New(nil, adapter.Config{Namespace: "app"}); e == nil {
		t.Fatal("nil client")
	}
	for _, cfg := range []adapter.Config{{}, {Namespace: " "}, {Namespace: "app", Options: cache.Options{MaxValueBytes: -1}}} {
		if _, e := adapter.New(&fake{}, cfg); e == nil {
			t.Fatal("invalid config")
		}
	}
}

func TestRedisNamespaceBounds(t *testing.T) {
	for _, namespace := range []string{" app", "app ", strings.Repeat("x", 513)} {
		if _, err := adapter.New(&fake{}, adapter.Config{Namespace: namespace}); err == nil {
			t.Fatalf("accepted namespace %q", namespace)
		}
	}
	if _, err := adapter.New(&fake{}, adapter.Config{Namespace: strings.Repeat("x", 512)}); err != nil {
		t.Fatal(err)
	}
}
