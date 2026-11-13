# 访问日志与协作截止

`http/middleware` 提供请求完成观测、协作式超时与并发准入,不提供速率配额、CORS 或压缩。组合入口通常
是 [router](../router/README.md) 与 `router.Options.Middleware`;本包也可独立装配。

```go
r.Use(middleware.AccessLog(logger, middleware.AccessOptions{OnRequest: metrics.Observe}))
r.Group(func(g chi.Router) {
    g.Use(middleware.Deadline(10 * time.Second))
    // 普通 API 组
})
```

## AccessLog 与 RequestEvent

`AccessLog` 注入请求 logger(带 trace_id)并在请求结束时记录一条完成日志,同时产出
`RequestEvent`。`OnRequest` 回调必须快速且并发安全;回调 panic 被记录并隔离,不影响响应。
接入 operation recorder 时，Duration 复用 handler 结束时间，不包含操作日志参数整理与输出耗时。

- `Route` 是路由模板(如 `/users/{id}`),不是原始路径;未匹配时为 `<unmatched>`。
- `Path` 是实际请求路径，保留 URL 编码，不含 query；超过 2048 字节时为 `<path>`。
  访问日志同时输出 path 和 route，Path 不用作指标标签。
- `MetricMethod` 是九个标准方法之一或 `_OTHER`;原始方法保留在 `Method` 与日志里,不进指标标签。
- `Status == 0` 表示取消或中止前没有任何提交;hijacked 连接不算已提交。
- `Canceled` 只反映请求 context 取消,单独的业务操作取消不算;`Aborted` 表示 panic 中止。
- 完成日志按状态分级:5xx 或中止为 ERROR,其余为 INFO。纯客户端取消不是服务错误,访问日志
  仍是唯一完成记录(见 [respond](../respond/README.md) 的取消语义)。

标签纪律:指标标签只允许 `Route` 与 `MetricMethod`;禁止 trace_id、用户 ID、完整 URL 或原始
自定义方法。访问日志不记录 query、body、认证头;日志字段由请求上下文 logger 承接。

`QuietHealth` 静默 `/healthz`、`/readyz` 的 2xx/3xx 完成日志,但 `OnRequest` 观测仍然执行;
挂载在子路由前缀下按路由相对路径匹配,不因挂载而失效。

## Deadline

`Deadline(timeout)` 设置协作式请求截止:超时仅取消 context,不强制终止 handler,不缓存或缓冲
流式响应。`respond.Adapter` 在提交前把到期映射为 504;原生 `http.Handler` 自行检查
`ctx.Err()`。`timeout <= 0` 直接透传。混合流式路由时应关闭全局 Timeout,普通 API 组用本
中间件,SSE 组自行管理断开与心跳。

## Admission

`Admission` 限制 handler 并发执行及等待准入的数量，并为排队设置独立超时：

```go
limiter, err := concurrency.NewBoundedLimiter(32, 16)
if err != nil { return err }
admission, err := middleware.Admission(limiter, middleware.AdmissionOptions{
    WaitTimeout: 100 * time.Millisecond,
    OnAdmission: metrics.ObserveAdmission, // 可选；观察 Duration 与 Err
})
if err != nil { return err }
r.Group(func(g chi.Router) {
    g.Use(admission)
    // 挂载需要保护的业务路由，健康/就绪探针在该组之外。
})
```

等待位满或准入等待超时返回共享 JSON 503；请求自身 deadline 到期返回 504，取消不写响应。
成功准入后使用原请求 context；准入超时不会缩短业务执行预算。正常返回或 panic 都会释放
执行位。handler 忽略 context 时无法强制停止；SSE、下载等长请求会一直持有执行位直到返回，
应单独装配容量策略。多个路由组复用同一 limiter 时共享容量，多个副本各自独立。

构造时拒绝 nil、无界等待 limiter、负超时；存在等待位时要求正的 WaitTimeout。
maxWaiters=0 可配 WaitTimeout=0，表示容量不足立即拒绝。等待不保证 FIFO；该能力不是
每用户速率限制，503 表示服务容量不足，不作为客户端额度超限的 429。

`limiter.Stats()` 可采集执行数、等待数和累计容量拒绝数；取消/等待超时不计入容量拒绝。
`OnAdmission` 每次准入尝试调用一次，Duration 是准入总耗时（包括排队），Err 为 nil 表示
成功；可按结果计数并记录耗时分布。回调应快速且并发安全，panic 记录并隔离。
排队超时与容量拒绝都应结合 HTTP 503 计数观察；请求完成指标继续用 AccessLog。
不要将请求路径、用户 ID 或错误文本直接用作指标标签。

准入策略不限制 HTTP 服务器已创建的连接或瞬时 goroutine 数量。生产应同时管理入口负载、
连接/读取超时及上游重试预算，避免客户端无退避重试。容量与等待预算按目标负载实测确定。
