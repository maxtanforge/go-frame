package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/cache"
)

type store struct {
	data    []byte
	hit     bool
	err     error
	written bool
}

func (s *store) Get(context.Context, string) ([]byte, bool, error) { return s.data, s.hit, s.err }
func (s *store) Set(_ context.Context, _ string, b []byte, _ time.Duration) error {
	s.data = b
	s.written = true
	return s.err
}
func (s *store) Delete(context.Context, string) error { return s.err }
func TestJSON(t *testing.T) {
	ctx := context.Background()
	s := &store{}
	if _, ok, e := cache.GetJSON[*int](ctx, s, "k"); ok || e != nil {
		t.Fatal(ok, e)
	}
	s.hit = true
	s.data = []byte("null")
	if v, ok, e := cache.GetJSON[*int](ctx, s, "k"); v != nil || !ok || e != nil {
		t.Fatal(v, ok, e)
	}
	s.data = []byte("bad")
	if _, ok, e := cache.GetJSON[int](ctx, s, "k"); !ok || e == nil {
		t.Fatal("decode error must preserve hit", ok, e)
	}
	s.data = []byte("123")
	if _, ok, e := cache.GetJSON[int](ctx, s, "k", cache.Options{MaxValueBytes: 2}); !ok || !errors.Is(e, cache.ErrValueTooLarge) {
		t.Fatal(ok, e)
	}
	backend := errors.New("backend")
	s.err = backend
	if _, _, e := cache.GetJSON[int](ctx, s, "k"); !errors.Is(e, backend) {
		t.Fatal(e)
	}
	if e := cache.SetJSON(ctx, s, "k", 1, 0); !errors.Is(e, backend) {
		t.Fatal(e)
	}
	s.err = nil
	s.written = false
	if e := cache.SetJSON(ctx, s, "k", 123, 0, cache.Options{MaxValueBytes: 2}); !errors.Is(e, cache.ErrValueTooLarge) || s.written {
		t.Fatal(e)
	}
	if e := cache.SetJSON(ctx, s, "k", make(chan int), 0); e == nil {
		t.Fatal("marshal accepted")
	}
	if e := cache.SetJSON(ctx, s, "k", 42, time.Millisecond); e != nil || string(s.data) != "42" {
		t.Fatal(string(s.data), e)
	}
	if _, _, e := cache.GetJSON[int](ctx, s, "", cache.Options{}); !errors.Is(e, cache.ErrInvalidKey) {
		t.Fatal(e)
	}
	if e := cache.SetJSON(ctx, s, "k", 42, 1); !errors.Is(e, cache.ErrInvalidTTL) {
		t.Fatal(e)
	}
	if _, _, e := cache.GetJSON[int](ctx, s, "k", cache.Options{MaxValueBytes: -1}); e == nil {
		t.Fatal("negative limit accepted")
	}
}
