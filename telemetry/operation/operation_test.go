package operation_test

import (
	"encoding/json"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	"strings"
	"testing"
)

func TestSafeFieldSnapshot(t *testing.T) {
	fields := map[string]any{"count": 1}
	got, err := operation.CopyFields(fields)
	if err != nil {
		t.Fatal(err)
	}
	fields["count"] = 2
	if got["count"] != 1 {
		t.Fatal("shared fields")
	}
	for _, bad := range []map[string]any{{"path": "/fake"}, {"actor_id": "fake"}, {"password": "secret"}, {"nested": map[string]any{"a": 1}}, {"field": strings.Repeat("x", 513)}, {"": "value"}} {
		if _, err := operation.CopyFields(bad); err == nil {
			t.Fatal("unsafe fields accepted")
		}
	}
}

func TestRequestSnapshotCopiesAndBoundsJSON(t *testing.T) {
	request := json.RawMessage(`{"query":{"page":["1"]}}`)
	event := operation.Event{Action: "read", Outcome: operation.Completed, Request: request}
	snapshot, err := event.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	request[0] = '!'
	if !json.Valid(snapshot.Request) {
		t.Fatal("snapshot shares mutable request bytes")
	}
	for _, value := range []json.RawMessage{json.RawMessage(`{"broken"`), json.RawMessage(`"` + strings.Repeat("x", 17<<10) + `"`)} {
		event.Request = value
		if _, err := event.Snapshot(); err == nil {
			t.Fatal("invalid or oversized request accepted")
		}
	}
}
