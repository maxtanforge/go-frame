package greeting

import (
	"context"
	"errors"
	"testing"
)

func TestGreetingRules(t *testing.T) {
	s := Service{Prefix: "Hello"}
	got, err := s.Greet(context.Background(), " Ada ")
	if err != nil || got.Message != "Hello, Ada" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := s.Greet(context.Background(), "  "); !errors.Is(err, ErrName) {
		t.Fatalf("empty name: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Greet(ctx, "Ada"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
