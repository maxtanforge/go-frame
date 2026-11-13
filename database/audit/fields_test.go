package audit

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestActorValidation(t *testing.T) {
	for _, a := range []Actor{{Kind: Subject, ID: "user:123"}, {Kind: System, ID: "import-job"}, {Kind: Anonymous}, {Kind: Subject, ID: "用户"}} {
		if err := a.Validate(); err != nil {
			t.Fatal(a, err)
		}
	}
	for _, a := range []Actor{{}, {Kind: "secret-kind", ID: "secret"}, {Kind: Subject}, {Kind: System, ID: " secret"}, {Kind: System, ID: "secret "}, {Kind: Anonymous, ID: "secret"}, {Kind: Subject, ID: "secret\n"}, {Kind: Subject, ID: "se\x00cret"}, {Kind: Subject, ID: string([]byte{0xff})}, {Kind: Subject, ID: strings.Repeat("x", 129)}} {
		if err := a.Validate(); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid actor: %v", err)
		}
	}
}

func TestNewNormalizesOneTimestamp(t *testing.T) {
	actor := Actor{Kind: System, ID: "job"}
	input := time.Date(2026, 9, 28, 12, 34, 56, 123456789, time.FixedZone("east", 8*3600))
	f, err := New(actor, input)
	want := time.Date(2026, 9, 28, 4, 34, 56, 123000000, time.UTC)
	if err != nil || f.CreatedAt != want || f.UpdatedAt != want || f.CreatedBy != actor || f.UpdatedBy != actor || f.Validate() != nil {
		t.Fatal(f, err)
	}
}

func TestUpdatePreservesCreation(t *testing.T) {
	at := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	f, _ := New(Actor{Kind: Subject, ID: "creator"}, at)
	before := f
	nextActor := Actor{Kind: System, ID: "updater"}
	updated, err := f.WithUpdate(nextActor, at.Add(time.Hour+1234567*time.Nanosecond))
	if err != nil || f != before || updated.CreatedAt != f.CreatedAt || updated.CreatedBy != f.CreatedBy || updated.UpdatedBy != nextActor || updated.UpdatedAt != at.Add(time.Hour+time.Millisecond) {
		t.Fatal(updated, err)
	}
	if _, err := updated.WithUpdate(nextActor, at); !errors.Is(err, ErrInvalid) {
		t.Fatal("time regression accepted", err)
	}
	if _, err := updated.WithUpdate(nextActor, updated.UpdatedAt); err != nil {
		t.Fatal("equal time rejected", err)
	}
	if _, err := (Fields{}).WithUpdate(nextActor, at); !errors.Is(err, ErrInvalid) {
		t.Fatal("uninitialized history accepted", err)
	}
}

func TestTimeBoundsAndInvalidHistory(t *testing.T) {
	actor := Actor{Kind: System, ID: "job"}
	for _, at := range []time.Time{{}, time.Date(999, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1000, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 3600))} {
		if _, err := New(actor, at); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid time", err)
		}
	}
	for _, at := range []time.Time{time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)} {
		if _, err := New(actor, at); err != nil {
			t.Fatal("valid bound", err)
		}
	}
	f, _ := New(actor, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	for _, change := range []func(*Fields){func(f *Fields) { f.CreatedBy = Actor{} }, func(f *Fields) { f.UpdatedBy = Actor{} }, func(f *Fields) { f.UpdatedAt = f.CreatedAt.Add(-time.Second) }, func(f *Fields) { f.CreatedAt = f.CreatedAt.In(time.FixedZone("zero", 0)) }, func(f *Fields) { f.UpdatedAt = f.UpdatedAt.Add(time.Nanosecond) }} {
		bad := f
		change(&bad)
		if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid history", err)
		}
		if _, err := bad.WithUpdate(actor, f.CreatedAt.Add(time.Hour)); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid history silently repaired", err)
		}
	}
}
