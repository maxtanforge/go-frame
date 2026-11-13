# 横切基础能力装配

本示例为无其他消费者的基础能力提供真实调用点,全部参与 `go build ./...` 编译验证;除
Redis 依赖项外均在测试中真实运行。

| 能力 | 入口 | 说明 |
|---|---|---|
| 进程内缓存 | `NewMemoryCache` / `Cached` | 有界 LRU + TTL,读穿透助手与来源无关(`cache.Store`) |
| Redis 缓存 | `NewRedisCache` | 借用既有客户端,使用 toolkit 命名空间;构造期不连接 |
| Redis 租约锁 | `NewRedisLocker` / `WithLease` | 借用客户端;失锁取消业务 context,释放错误与业务错误一并返回 |
| 并发准入 | `NewLimiter` | 进程内执行与等待数量分别不超过 capacity,等待预算由 context 控制 |
| 定时任务 | `NewPlanner` / `EventLogger` | 显式注册、显式观察;Run 阻塞到 Shutdown,不隐式启动 |
| 出站 HTTP | `NewFetcher` / `GetJSON` | 自有客户端;非 2xx 映射为公开安全文案,不外泄上游响应体 |
| 中间件直用 | `NewHTTP` | 裸 chi 上直接装配 trace 关联、访问日志与协作 deadline,不经 router.New |
| 响应适配 | `NewHTTP` 内 `respond.Adapter` | 直接消费 HTTP 响应能力包 |

## 边界

- Redis 缓存与租约锁只构造不连接:真实交互由 `cache/redis`、`lock/redis` 的包测试
  与显式开启的集成测试覆盖;示例不内嵌 Redis。
- `Cached` 不做 singleflight:并发未命中会各自加载,击穿防护由业务决定(见路线图 E2)。
- 业务域键、TTL、回源与失效政策见 [cache 用法](../../cache/README.md#业务键与失败政策)。Cached 在缓存读取、来源加载或回填失败时返回错误；成功更新权威来源后如何失效由业务决定。
- `WithLease` 的一次 Acquire 不等待(wait=false);需要排队等待时由调用方设置 context
  预算并传 wait=true。
- `GetJSON` 不做重试;http 仅可能透明重放其认为可重放的请求。

## 组合

```go
handler := toolkit.NewHTTP(logger, func(_ context.Context, e middleware.RequestEvent) {
    metrics.Observe(e.Route, e.MetricMethod, e.Status, e.Duration)
})
planner, err := toolkit.NewPlanner(jobs, toolkit.EventLogger(logger))
// scheduler 作为 app.Service 交给应用生命周期,见 background 示例
```
