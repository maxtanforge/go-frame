package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNilContextLoggerFallsBack(t *testing.T) {
	if got := FromContext(IntoContext(context.Background(), nil)); got != slog.Default() {
		t.Fatal("nil logger returned")
	}
}

func TestProvidedStackIsNotOverwritten(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		var output bytes.Buffer
		l := slog.New(buildHandler(Options{Format: "json", StackOnError: true}, &output))
		if inherited {
			l = l.With("stack", "origin-stack")
			l.Error("failure")
		} else {
			l.Error("failure", "stack", "origin-stack")
		}
		var record map[string]any
		if err := json.Unmarshal(output.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if record["stack"] != "origin-stack" {
			t.Fatal("original stack overwritten")
		}
		if bytes.Count(output.Bytes(), []byte(`"stack":`)) != 1 {
			t.Fatal("duplicate stack key")
		}
	}
}
