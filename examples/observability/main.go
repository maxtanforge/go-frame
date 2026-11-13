// Observability demonstrates explicit SDK ownership without an external collector.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/http/server"
	"github.com/maxtanforge/go-frame/identity"
	"github.com/maxtanforge/go-frame/telemetry/metric"
	metricotel "github.com/maxtanforge/go-frame/telemetry/metric/otel"
	"github.com/maxtanforge/go-frame/telemetry/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		slog.Error("observability example stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) (runErr error) {
	meta, err := identity.New(identity.Options{Service: "observability", Version: "dev", Environment: "dev", InstanceID: os.Getenv("INSTANCE_ID")})
	if err != nil {
		return err
	}
	adapter, recorder, shutdown, err := newTelemetry(meta, os.Stdout)
	if err != nil {
		return err
	}
	stopped := false
	stopTelemetry := func(ctx context.Context) error { stopped = true; return shutdown(ctx) }
	// Covers failures before App takes ownership, including cancellation before
	// the first hook starts. Logging remains available throughout cleanup.
	defer func() {
		if !stopped {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			runErr = errors.Join(runErr, stopTelemetry(cleanup))
		}
	}()
	lg := slog.New(telemetry.LogHandler(slog.NewJSONHandler(os.Stderr, nil).WithAttrs(meta.Attrs())))
	resources, err := metric.NewResourceRecorder()
	if err != nil {
		return err
	}
	routes := router.New(lg, router.Options{
		Middleware: []func(http.Handler) http.Handler{telemetry.Route},
		// 双写:本地定桶直方图 + otel 导出;网关从 /metrics 抓取文本格式。
		OnRequest: func(ctx context.Context, e middleware.RequestEvent) {
			if err := recorder.Observe(ctx, metric.Observation{Route: e.Route, Method: e.MetricMethod, DurationMS: float64(e.Duration.Microseconds()) / 1000}); err != nil {
				lg.ErrorContext(ctx, "record request metric", "error", err)
			}
		},
	})
	routes.Get("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, recorder.Prometheus()+resources.Prometheus())
	})
	routes.Get("/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		lg.InfoContext(r.Context(), "order example handled")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	service := server.New("127.0.0.1:3001", adapter.Handler(meta.Service, routes))
	application, err := app.New(service, app.Options{
		ShutdownTimeout: 10 * time.Second, CleanupReserve: 2 * time.Second,
		// First hook stops last. Register database/client hooks after this one.
		Hooks: []app.Hook{{Name: "telemetry", Stop: stopTelemetry}},
	})
	if err != nil {
		return err
	}
	lg.Info("listening", "address", "127.0.0.1:3001")
	return application.Run(ctx)
}

// newTelemetry returns an explicit adapter and an idempotent SDK shutdown.
// It installs no global providers. Production deployments can replace the stdout
// exporters with OTLP exporters while retaining this ownership and budget model.
func newTelemetry(meta identity.Metadata, output io.Writer) (*telemetry.Adapter, *metric.RequestRecorder, func(context.Context) error, error) {
	writer := &lockedWriter{writer: output}
	traceExporter, err := stdouttrace.New(stdouttrace.WithWriter(writer))
	if err != nil {
		return nil, nil, nil, err
	}
	metricExporter, err := stdoutmetric.New(stdoutmetric.WithWriter(writer))
	if err != nil {
		return nil, nil, nil, errors.Join(err, traceExporter.Shutdown(context.Background()))
	}
	res := resource.NewSchemaless(
		attribute.String("service.name", meta.Service),
		attribute.String("service.version", meta.Version),
		attribute.String("service.instance.id", meta.InstanceID),
		attribute.String("deployment.environment.name", meta.Environment),
	)
	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))),
		sdktrace.WithBatcher(traceExporter))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(5*time.Second))))
	var once sync.Once
	var stopErr error
	shutdown := func(ctx context.Context) error {
		once.Do(func() { stopErr = errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx)) })
		return stopErr
	}
	adapter, err := telemetry.New(telemetry.Options{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return nil, nil, nil, errors.Join(err, shutdown(cleanup))
	}
	recorder, err := metricotel.NewRequestRecorder(mp.Meter("frame"), metric.Options{})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return nil, nil, nil, errors.Join(err, shutdown(cleanup))
	}
	return adapter, recorder, shutdown, nil
}

// Both exporters may write concurrently. Keep each JSON export write intact.
type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
