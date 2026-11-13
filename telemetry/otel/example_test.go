package telemetry_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/maxtanforge/go-frame/telemetry/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func ExampleLogHandler() {
	provider := sdktrace.NewTracerProvider()
	defer func() { _ = provider.Shutdown(context.Background()) }()
	adapter, err := telemetry.New(telemetry.Options{TracerProvider: provider})
	if err != nil {
		panic(err)
	}
	var output bytes.Buffer
	lg := slog.New(telemetry.LogHandler(slog.NewJSONHandler(&output, nil)))
	handler := adapter.Handler("example", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lg.InfoContext(r.Context(), "handled request")
		w.WriteHeader(http.StatusOK)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	fmt.Println(response.Code, strings.Contains(output.String(), "otel_trace_id"))
	// Output: 200 true
}
