package dev

import (
	"errors"
	"testing"
	"time"
)

func TestDrainWaitsForDescendants(t *testing.T) {
	checks := 0
	if err := waitForDrain(time.Second, func() (bool, error) { checks++; return checks == 3, nil }); err != nil {
		t.Fatal(err)
	}
	if checks != 3 {
		t.Fatalf("returned while descendants active: %d", checks)
	}
}

func TestDrainTimeoutAndQueryFailure(t *testing.T) {
	if err := waitForDrain(time.Millisecond, func() (bool, error) { return false, nil }); err == nil {
		t.Fatal("active descendants reported stopped")
	}
	queryErr := errors.New("query failed")
	if err := waitForDrain(time.Second, func() (bool, error) { return false, queryErr }); !errors.Is(err, queryErr) {
		t.Fatalf("query error lost: %v", err)
	}
}

func TestConcurrentCleanupHasBoundedWait(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	p := &commandProcess{cleanup: func(time.Duration) error { <-release; return nil }}
	result := make(chan error, 1)
	go func() { result <- p.clean(time.Millisecond) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("active cleanup reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup wait exceeded budget")
	}
	go func() { result <- p.clean(time.Millisecond) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("concurrent cleanup reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent cleanup blocked behind first caller")
	}
}
