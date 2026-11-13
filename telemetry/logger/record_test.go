package logger

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A handler may receive copies of the same record from an application fan-out.
// Adding a stack must not alter backing storage shared with another recipient.
func TestStackHandlerCanReceiveSharedRecord(t *testing.T) {
	var output bytes.Buffer
	handler := stackHandler{Handler: slog.NewJSONHandler(&output, nil)}
	for count := 6; count <= 24; count++ {
		record := slog.NewRecord(time.Now(), slog.LevelError, "fan-out", 0)
		for i := 0; i < count; i++ {
			record.AddAttrs(slog.Int(fmt.Sprintf("field_%d", i), i))
		}
		for range 2 {
			if err := handler.Handle(context.Background(), record); err != nil {
				t.Fatal(err)
			}
		}
		if record.NumAttrs() != count {
			t.Fatal("original record changed")
		}
	}
	if strings.Contains(output.String(), `"!BUG"`) {
		t.Fatal("stack handler modified shared record storage")
	}
	if got := strings.Count(output.String(), `"stack":`); got != 38 {
		t.Fatalf("stack count = %d, want 38", got)
	}
}
