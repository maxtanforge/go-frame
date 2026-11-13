// Package operation defines bounded, structured operation events and sinks.
package operation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalid reports metadata that violates the Event contract.
var ErrInvalid = errors.New("operation: invalid event")

// Outcome classifies how an operation ended.
type Outcome string

// Terminal outcomes recorded on every event.
const (
	Completed Outcome = "completed"
	Denied    Outcome = "denied"
	Failed    Outcome = "failed"
	Canceled  Outcome = "canceled"
	Aborted   Outcome = "aborted"
	Hijacked  Outcome = "hijacked"
)

// Event describes HTTP execution, not proof of a database transaction commit.
// Fields contains selected scalar metadata. Request is bounded JSON;
// the HTTP recorder redacts captured query/body fields before publication.
type Event struct {
	Action       string          `json:"action"`
	Description  string          `json:"description,omitempty"`
	ResourceType string          `json:"resource_type,omitempty"`
	ResourceID   string          `json:"resource_id,omitempty"`
	ActorID      string          `json:"actor_id,omitempty"`
	ActorName    string          `json:"actor_name,omitempty"`
	ActorIssuer  string          `json:"actor_issuer,omitempty"`
	TraceID      string          `json:"trace_id"`
	Method       string          `json:"method"`
	Path         string          `json:"path"` // Encoded request path without query.
	Route        string          `json:"route"`
	Timestamp    int64           `json:"timestamp"`
	DurationMS   float64         `json:"duration_ms"`
	Status       int             `json:"status"`
	Outcome      Outcome         `json:"outcome"`
	Fields       map[string]any  `json:"fields,omitempty"`
	Request      json.RawMessage `json:"request,omitempty"`
}

// Sink receives one event per operation; Record must be safe for concurrent use.
type Sink interface {
	Record(context.Context, Event) error
}

// SinkFunc adapts a plain function to Sink.
type SinkFunc func(context.Context, Event) error

// Record implements Sink by delegating to the function.
func (f SinkFunc) Record(ctx context.Context, e Event) error { return f(ctx, e) }

// TextValid validates bounded metadata without accepting control characters.
func TextValid(s string, maxLen int) bool {
	if len(s) > maxLen || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

var sensitiveKeyNormalizer = strings.NewReplacer("_", "", "-", "")

// SensitiveKey identifies built-in secret key patterns for request redaction and
// operation metadata validation. It ignores case, underscores and hyphens.
func SensitiveKey(key string) bool {
	key = sensitiveKeyNormalizer.Replace(strings.ToLower(key))
	for _, fragment := range []string{"password", "token", "secret", "apikey", "authorization", "cookie"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

// CopyFields validates and snapshots up to 16 scalar values. Callers must not
// mutate the supplied map concurrently. Known sensitive key patterns are rejected;
// callers remain responsible for secrets stored under unrelated names.
func CopyFields(fields map[string]any) (map[string]any, error) {
	if len(fields) > 16 {
		return nil, ErrInvalid
	}
	if len(fields) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		if len(k) == 0 || len(k) > 64 {
			return nil, ErrInvalid
		}
		for i, c := range k {
			if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
				return nil, ErrInvalid
			}
		}
		if SensitiveKey(k) {
			return nil, ErrInvalid
		}
		switch strings.ToLower(k) {
		case "action", "description", "actor_id", "actor_name", "actor_issuer", "trace_id", "timestamp", "duration_ms", "status", "outcome", "method", "path", "route", "request", "resource_id", "resource_type":
			return nil, ErrInvalid
		}
		switch value := v.(type) {
		case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		case string:
			if !TextValid(value, 512) {
				return nil, ErrInvalid
			}
		case float64:
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, ErrInvalid
			}
		case float32:
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
		out[k] = v
	}
	return out, nil
}

// Snapshot validates the event and copies mutable metadata.
func (e Event) Snapshot() (Event, error) {
	for _, f := range []struct {
		s string
		n int
	}{{e.Action, 256}, {e.Description, 512}, {e.ResourceType, 128}, {e.ResourceID, 128}, {e.ActorID, 128}, {e.ActorName, 128}, {e.ActorIssuer, 256}, {e.TraceID, 128}, {e.Method, 32}, {e.Path, 2048}, {e.Route, 2048}} {
		if !TextValid(f.s, f.n) {
			return Event{}, ErrInvalid
		}
	}
	if e.Action == "" || e.Timestamp < 0 || e.DurationMS < 0 || math.IsNaN(e.DurationMS) || math.IsInf(e.DurationMS, 0) || e.Status < 0 || e.Status > 599 {
		return Event{}, ErrInvalid
	}
	switch e.Outcome {
	case Completed, Denied, Failed, Canceled, Aborted, Hijacked:
	default:
		return Event{}, ErrInvalid
	}
	fields, err := CopyFields(e.Fields)
	if err != nil {
		return Event{}, err
	}
	e.Fields = fields
	if len(e.Request) > 16<<10 || len(e.Request) > 0 && !json.Valid(e.Request) {
		return Event{}, ErrInvalid
	}
	e.Request = append(json.RawMessage(nil), e.Request...)
	return e, nil
}

// SlogSink writes events as structured slog records.
type SlogSink struct{ logger *slog.Logger }

// NewSlogSink adapts a logger; nil selects slog.Default.
func NewSlogSink(lg *slog.Logger) *SlogSink {
	if lg == nil {
		lg = slog.Default()
	}
	return &SlogSink{logger: lg}
}

// Record snapshots the event and emits one slog record per operation.
func (s *SlogSink) Record(ctx context.Context, e Event) error {
	e, err := e.Snapshot()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	level := slog.LevelInfo
	if e.Outcome == Failed || e.Outcome == Aborted {
		level = slog.LevelError
		if e.Outcome == Failed && e.Status >= 400 && e.Status < 500 {
			level = slog.LevelWarn
		}
	}
	s.logger.Log(ctx, level, "operation completed", "kind", "operation", "event", e)
	return nil
}
