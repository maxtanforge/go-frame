package devicesync

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// The test store models the durable contract; PostgreSQL statements are tested
// separately. Recreating Service must not erase pending deliveries.
type memoryStore struct {
	mu          sync.Mutex
	records     map[string]Record
	completeErr error
}

func (s *memoryStore) Put(ctx context.Context, event Event) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if old, ok := s.records[event.ID]; ok {
		if old.Event != event {
			return Record{}, ErrConflict
		}
		return old, nil
	}
	r := Record{Event: event}
	s.records[event.ID] = r
	return r, nil
}
func (s *memoryStore) Find(ctx context.Context, id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	r, ok := s.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}
func (s *memoryStore) Complete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.completeErr != nil {
		return s.completeErr
	}
	r, ok := s.records[id]
	if !ok {
		return ErrNotFound
	}
	r.Delivered = true
	s.records[id] = r
	return nil
}

type sinkFunc func(context.Context, Event) error

func (f sinkFunc) Save(ctx context.Context, e Event) error { return f(ctx, e) }

func TestFailedDeliveryIsRecoverableAcrossServiceRestart(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{records: map[string]Record{}}
	remoteErr := errors.New("remote unavailable")
	first := Service{Store: store, Sink: sinkFunc(func(context.Context, Event) error { return remoteErr })}
	event := Event{ID: "vivo:user-1:event-1", UserID: "user-1", Steps: 0}
	accepted, err := first.Accept(ctx, event)
	if err != nil || accepted.Delivered {
		t.Fatalf("accept: %+v %v", accepted, err)
	}
	if err = first.Deliver(ctx, event.ID); !errors.Is(err, remoteErr) {
		t.Fatal(err)
	}
	if r, _ := store.Find(ctx, event.ID); r.Delivered {
		t.Fatal("failed delivery marked complete")
	}
	calls := 0
	restarted := Service{Store: store, Sink: sinkFunc(func(_ context.Context, got Event) error {
		calls++
		if got != event {
			t.Fatalf("replay changed event: %+v", got)
		}
		return nil
	})}
	if _, err = restarted.Accept(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Deliver(ctx, event.ID); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Deliver(ctx, event.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("completed event sent %d times", calls)
	}
}

func TestRemoteSuccessLocalFailureReusesIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	commitErr := errors.New("completion result unknown")
	store := &memoryStore{records: map[string]Record{}, completeErr: commitErr}
	event := Event{ID: "vivo:user-1:event-2", UserID: "user-1", Steps: 20}
	remote := map[string]Event{}
	calls := 0
	svc := Service{Store: store, Sink: sinkFunc(func(_ context.Context, got Event) error {
		calls++
		if old, ok := remote[got.ID]; ok && old != got {
			t.Fatal("same idempotency key changed payload")
		}
		remote[got.ID] = got
		return nil
	})}
	if _, err := svc.Accept(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deliver(ctx, event.ID); !errors.Is(err, commitErr) {
		t.Fatal(err)
	}
	store.completeErr = nil
	if err := svc.Deliver(ctx, event.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(remote) != 1 {
		t.Fatalf("attempts=%d effects=%d", calls, len(remote))
	}
}

func TestAcceptRejectsChangedEventAndInvalidInput(t *testing.T) {
	store := &memoryStore{records: map[string]Record{}}
	svc := Service{Store: store}
	event := Event{ID: "event-1", UserID: "user-1", Steps: 1}
	if _, err := svc.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.Steps++
	if _, err := svc.Accept(context.Background(), event); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for _, input := range []Event{{}, {ID: "event-2", UserID: "user-1", Steps: -1}} {
		if _, err := svc.Accept(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestCanceledDeliveryRemainsPending(t *testing.T) {
	store := &memoryStore{records: map[string]Record{}}
	ctx, cancel := context.WithCancel(context.Background())
	svc := Service{Store: store, Sink: sinkFunc(func(ctx context.Context, _ Event) error { cancel(); return ctx.Err() })}
	event := Event{ID: "event-1", UserID: "user-1", Steps: 1}
	if _, err := svc.Accept(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deliver(ctx, event.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r, _ := store.Find(context.Background(), event.ID); r.Delivered {
		t.Fatal("canceled work completed")
	}
}
