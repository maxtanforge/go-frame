package operation_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/telemetry/operation"
)

func TestSlogFailureLevels(t *testing.T) {
	for _, tc := range []struct {
		status  int
		outcome operation.Outcome
		level   string
	}{{404, operation.Failed, "WARN"}, {409, operation.Failed, "WARN"}, {422, operation.Failed, "WARN"}, {500, operation.Failed, "ERROR"}, {0, operation.Failed, "ERROR"}, {400, operation.Aborted, "ERROR"}, {401, operation.Denied, "INFO"}, {403, operation.Denied, "INFO"}, {200, operation.Completed, "INFO"}} {
		var logs strings.Builder
		sink := operation.NewSlogSink(slog.New(slog.NewTextHandler(&logs, nil)))
		if err := sink.Record(context.Background(), operation.Event{Action: "test", Status: tc.status, Outcome: tc.outcome}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(logs.String(), "level="+tc.level) {
			t.Fatalf("status %d outcome %s: %s", tc.status, tc.outcome, logs.String())
		}
	}
}

func TestSensitiveFieldPatterns(t *testing.T) {
	for _, key := range []string{"Password", "userToken", "refresh_token", "clientSecret", "API_KEY", "sessionCookie", "proxyAuthorization"} {
		if _, err := operation.CopyFields(map[string]any{key: "private"}); err == nil {
			t.Fatalf("accepted sensitive key %s", key)
		}
	}
	for _, key := range []string{"count", "accountId", "name"} {
		if _, err := operation.CopyFields(map[string]any{key: "public"}); err != nil {
			t.Fatalf("rejected ordinary key %s: %v", key, err)
		}
	}
}
