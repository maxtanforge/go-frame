package id

import (
	"errors"
	"testing"
	"time"
)

func TestRollbackRecoversWithoutReusingID(t *testing.T) {
	sf, err := NewSnowflake(1)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.UnixMilli(epoch + 100)
	sf.now = func() time.Time { return clock }
	first, err := sf.Next()
	if err != nil {
		t.Fatal(err)
	}
	clock = time.UnixMilli(epoch + 99)
	if _, err := sf.Next(); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("expected explicit rollback error, got %v", err)
	}
	clock = time.UnixMilli(epoch + 100)
	recovered, err := sf.Next()
	if err != nil || recovered <= first {
		t.Fatalf("clock recovery must continue sequence without duplicate: %d, %d, %v", first, recovered, err)
	}
}
