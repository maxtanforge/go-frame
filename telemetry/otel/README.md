# Explicit HTTP telemetry

This optional adapter uses official `otelhttp v0.71.0` with OpenTelemetry API/SDK
`v1.46.0`, resolved from the module registry. See the official
[Go instrumentation guide](https://opentelemetry.io/docs/languages/go/instrumentation/)
and [otelhttp API](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/http/otelhttp).

An application supplies its own tracer provider. It can optionally supply a meter
provider and propagator. The default meter is no-op and the default propagator is
W3C TraceContext without baggage; no global providers or propagators are changed.

```go
adapter, err := telemetry.New(telemetry.Options{TracerProvider: provider})
if err != nil { return err }
lg := slog.New(telemetry.LogHandler(logs.Logger().Handler()))
routes := router.New(lg, router.Options{ // import server/router
    Middleware: []func(http.Handler) http.Handler{telemetry.Route},
})
// Register business routes before serving.
svc := server.New(":3000", adapter.Handler("orders-api", routes))
client, err := httpclient.New(httpclient.Options{WrapTransport: adapter.Transport})
if err != nil { return err }
```

Wrap the entire router, outside its access logger, so the server span context is
available to all middleware and handlers. Use a constant operation name rather
than a URL/path. Outbound calls must use the request context to join that trace.
Close or completely consume response bodies to complete client spans.

`Route` bridges chi's final route template to both the server span and otelhttp's
metric labeler. Install it once inside the router, with `Adapter.Handler` outside.
For example, `/orders/123` and `/orders/456` share `http.route=/orders/{id}`;
different templates retain separate metric series. Nested routes are joined after
execution. Unmatched root requests omit the attribute; partially matched mounts
may retain a bounded `/api/*` template. There is no raw-path fallback. The middleware
preserves streaming interfaces and panic propagation, including `ErrAbortHandler`.
It records the route on a span during unwinding, but upstream otelhttp does not
record its normal completion metrics when a panic escapes the wrapped handler.

`LogHandler` adds `otel_trace_id` and `otel_span_id` for valid span contexts without
replacing the existing `trace_id` correlation field. Configure it before deriving
loggers with WithGroup; fields follow the handler's current group. Standard slog
level filtering, attributes and groups remain available. These IDs are correlation
metadata, not trusted user identity.

The HTTP adapter omits known full-URL/query/legacy-target and exception-message
attributes; it clears span status descriptions and replaces recorded HTTP error
text. Requests are not rewritten. Paths, event names, custom attributes and
application-selected headers still need application privacy rules. This is a
narrow HTTP instrumentation filter, not a general exporter redaction boundary.

SDK/exporter construction, resources, sampling, flushing and shutdown belong to
the application. To flush after other resources, register the SDK provider's
Shutdown hook first so reverse-order cleanup runs it last. Keep the logging
runtime alive until App finishes. Handle failures that occur before App owns the
hooks by closing any already-created SDK/client resources in the composition root.
The adapter does not install an exporter, Collector, dashboard or signal handler.

The runnable [observability example](../../examples/observability/README.md) owns
explicit stdout exporters, resource attributes, sampling and bounded shutdown.
Reserve cleanup time with `app.Options.CleanupReserve` so an exhausted HTTP drain
does not consume the SDK's entire shutdown deadline. This remains a cooperative
budget; a blocking exporter or writer must support the deployment's shutdown needs.

The client transport decorator retains the privately owned base transport's
connection/time limits and cleanup. `Adapter.Transport(nil)` follows official
otelhttp behavior and uses `http.DefaultTransport`; use WrapTransport for owned
framework clients. Decorators with additional owned resources must arrange their
own lifecycle cleanup.
