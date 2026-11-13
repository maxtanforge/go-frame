# 健康探测核心

`Checker.Readiness(ctx)` 返回报告，`SetReady(false)` 控制排空。核心不导入 HTTP 或日志，
可以用于后台任务、启动协调和测试。判定是否接流量使用 Report.Status；遍历完整失败
原因使用 Report.Failures。当前 Checks 包含成功项与失败的可选项，不保证覆盖失败的
必需项；不要仅以 Checks 内容判断整体健康。HTTP 端点使用
[http/health](../http/health/README.md) 适配。


All checks are required by default. Use `Optional: true` only when the application
can actually serve requests correctly without that dependency:

```go
checker, err := health.New(health.Options{Checks: []health.Check{
    {Name: "database", Probe: db.Ping},
    {Name: "cache", Probe: cache.Ping, Optional: true},
}})
```

When using the HTTP adapter, required failure returns HTTP 503. If all required checks pass but optional checks
fail or time out, readiness returns HTTP 200 with data status `degraded` and the
affected check value `unavailable`; successful checks use `ok`. Raw error details are returned in Report.Failures (excluded from JSON); the HTTP adapter
logs them without placing them in the public response. Optional failures log at
warning level. `SetReady(false)` still returns 503; liveness is independent.

There is one total timeout per readiness call (default 1s). Two pools bound probes
across calls: `Concurrency` defaults to 4 required probes, `OptionalConcurrency`
defaults to 1 optional probe. The sum is the total possible active-probe bound.
A stuck optional probe occupies its optional slot until it returns, but cannot
starve required probes. Optional checks can still add up to the total timeout to
the readiness response; this package does not cache results or detach a scheduler.

Probes must be concurrency-safe and respond to context cancellation. The runtime
cannot terminate a callback that ignores cancellation. Calls waiting for a slot
stop at the request deadline; no unbounded active probe work is created.
Completed results already buffered at the deadline are retained. Missing results
remain unavailable, and a probe returning success after cancellation or its
deadline is treated as failed.
The absolute deadline is checked even if timer scheduling has not yet published
context cancellation. Non-nil callback errors retain their original cause.

Readiness policy does not change startup ownership. `resource.Hook()` runs any
configured startup Probe and propagates failures. An application that tolerates
an unavailable cache at startup needs an explicit startup/fallback strategy; setting a health
check to Optional does not silently make its resource initialization optional.

With `app/resource`, construct the check explicitly at the composition root:

```go
check := health.Check{
    Name: cacheResource.Name(),
    Probe: cacheResource.Probe,
    Optional: true,
}
```
