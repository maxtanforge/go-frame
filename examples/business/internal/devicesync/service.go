// Package devicesync demonstrates durable acceptance and replayable delivery.
// Event state and the receiver's idempotency contract belong to this business.
package devicesync

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrInvalid  = errors.New("invalid device event")
	ErrConflict = errors.New("event ID already has a different payload")
	ErrNotFound = errors.New("device event not found")
)

// Event uses a stable, source-and-user-scoped ID supplied by a trusted adapter.
// Generating a fresh ID on every callback would defeat duplicate detection.
type Event struct {
	ID     string
	UserID string
	Steps  int
}

// Validate protects the same invariants for callbacks and non-HTTP callers.
func (e Event) Validate() error {
	if strings.TrimSpace(e.ID) == "" || len(e.ID) > 128 || strings.TrimSpace(e.UserID) == "" || len(e.UserID) > 128 || e.Steps < 0 {
		return ErrInvalid
	}
	return nil
}

// Record distinguishes accepted input from acknowledged remote completion.
type Record struct {
	Event     Event
	Delivered bool
}

// Store atomically deduplicates immutable events. Put must reject a different
// payload under an existing ID. Complete is idempotent. Neither method implies
// a transaction spanning this store and the remote service.
type Store interface {
	Put(context.Context, Event) (Record, error)
	Find(context.Context, string) (Record, error)
	Complete(context.Context, string) error
}

// Sink must atomically deduplicate Event.ID at the receiver and reject changed
// payloads. Calls may overlap or repeat after an ambiguous response. Sending an
// Idempotency-Key header alone does not establish this server-side guarantee.
type Sink interface {
	Save(context.Context, Event) error
}

// Service is explicitly assembled with a durable store and an idempotent sink.
// Acceptance may be deployed separately from delivery, without a sink.
type Service struct {
	Store Store
	Sink  Sink
}

// Accept commits the event before reporting acceptance. A successful result
// with Delivered=false means pending work, not completed synchronization.
func (s Service) Accept(ctx context.Context, event Event) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if err := event.Validate(); err != nil {
		return Record{}, err
	}
	return s.Store.Put(ctx, event)
}

// Deliver performs one attempt. An error never reports successful delivery.
// After an ambiguous completion write, reread state before replaying the same
// original ID. No request-context detachment or automatic retry occurs.
func (s Service) Deliver(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := s.Store.Find(ctx, id)
	if err != nil {
		return err
	}
	if record.Delivered {
		return nil
	}
	if err = s.Sink.Save(ctx, record.Event); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return s.Store.Complete(ctx, id)
}
