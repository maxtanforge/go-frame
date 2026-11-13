# Explicit observability composition

Run from the repository root:

```sh
go run ./examples/observability
curl -H 'traceparent: 00-0123456789abcdef0123456789abcdef-0123456789abcdef-01' http://127.0.0.1:3001/orders/42
```

On PowerShell, use `curl.exe`. Stop with Ctrl+C to drain requests and flush the
providers. The example listens only on loopback. Traces and metrics are JSON on
stdout; application logs go to stderr. Metrics export every five seconds and on
shutdown. No Collector, database or Redis is required.

The composition root creates official stdout exporters and SDK providers without
installing global providers. Resource attributes include service name, version,
instance ID and deployment environment; set `INSTANCE_ID` for a stable replica
identity. Sampling is parent-based with a 10% root trace ratio. The sampled parent
header above makes a trace visible deterministically; metrics are not trace-sampled.
Choose a trust-boundary policy before accepting sampling decisions from untrusted
clients in a public production service.

`telemetry.Route` attaches the chi template `/orders/{id}` to spans and metric
series. Access logs include both framework correlation IDs and active OTel IDs.
The existing HTTP privacy filter removes full URLs and queries, but paths and
application attributes still require an application privacy policy.

The telemetry hook is registered first so it stops last. The ten-second shutdown
budget reserves two seconds for resource cleanup after draining HTTP. Both provider
shutdowns share the remaining deadline; adjust the reserve for exporter and other
resource costs. Cleanup also runs on errors before App owns the providers.
For production, replace stdout exporters with explicitly configured OTLP exporters
and tune sampling, exporter timeouts, batch limits and collector destinations.
Stdout is a self-contained demonstration, not a collector deployment.

## Pool observations

Register observable instruments in the composition root after acquiring the pool.
Use the explicit SDK meter provider, never a global meter. These local statistics
reads do not make network calls. For example, with `db` being `*database.DB`:

```go
meter := meterProvider.Meter("orders/pools")
inUse, err := meter.Int64ObservableGauge("orders.sql.connections.in_use")
if err != nil { return err }
registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
    observer.ObserveInt64(inUse, int64(db.SQLDB().Stats().InUse))
    return nil
}, inUse)
if err != nil { return err }
// Unregister before the pool closes; include Unregister errors in hook cleanup.
```

Import `go.opentelemetry.io/otel/metric` for `metric.Observer`. Keep the returned
registration for cleanup. SQL `OpenConnections` and `Idle` are gauges;
`WaitCount` and `WaitDuration` are cumulative counters (duration in seconds with
Float64 instruments). Observe Redis `cache.Client.PoolStats()` similarly:
`TotalConns` and `IdleConns` are gauges, while `Hits`, `Misses` and `Timeouts` are
cumulative counters. These are custom application metric names; document their
units and aggregation rules in dashboards.

Use bounded attributes such as a configured logical pool name. Do not label
metrics with DSNs, passwords, SQL text, key names, user IDs or request paths.
Unregister callbacks before resource shutdown; restart cumulative series when an
underlying pool is replaced. Do not use pool metrics as readiness probes. The
framework leaves pool ownership and metric registration explicit rather than
making database/Redis core packages depend on OpenTelemetry.

示例同时装配请求延迟指标:`metricotel.NewRequestRecorder` 把每次观测双写进本地定桶直方图与 otel 导出,`/metrics` 返回 Prometheus 文本格式供网关抓取;标签只用路由模板与标准方法。

v0.1.3 示例的 `/metrics` 同时输出请求延迟和 Go 运行时/进程资源指标。连接池可通过 `metric.SQLPool`、`metricredis.Pool` 在启动装配时注册；见 telemetry/metric 文档。资源指标不自动接入本示例的 OTel stdout 导出器。
