# 请求延迟指标

`telemetry/metric` 提供定桶累计直方图与请求维度记录器,纯标准库、无新依赖。与 http 解耦:
调用方在 `middleware.AccessOptions.OnRequest` 里把 `(e.Route, e.MetricMethod, e.Duration)` 喂给
`RequestRecorder.Observe`,telemetry 不反向依赖传输层。

```go
recorder, err := metric.NewRequestRecorder(metric.Options{})
if err != nil { return err }
routes := router.New(lg, router.Options{
    OnRequest: func(ctx context.Context, e middleware.RequestEvent) {
        if err := recorder.Observe(ctx, metric.Observation{
            Route: e.Route, Method: e.MetricMethod,
            DurationMS: float64(e.Duration.Microseconds())/1000,
        }); err != nil { lg.ErrorContext(ctx, "record request metric", "error", err) }
    },
})
routes.Get("/metrics", func(w http.ResponseWriter, _ *http.Request) {
    w.Header().Set("Content-Type", "text/plain; version=0.0.4")
    _, _ = io.WriteString(w, recorder.Prometheus())
})
```

- `Histogram`:升序有限非负桶 + 隐式 +Inf,`Observe`/`Snapshot`(累计桶、count、sum,
  独立拷贝),单个直方图快照保持桶、count、sum 一致；不同路由不承诺全局同一时刻快照
- `RequestRecorder`:按 `(Route, Method)` 键控独立直方图;总键上限 512,预留一个共享溢出键,超限路由与方法均折叠为
  `_OTHER`;`Options.Buckets` 默认毫秒延迟桶(1..5000),`Options.OnObserve` 供导出适配订阅
- `Prometheus()`:文本格式导出,固定指标名 `http_request_duration_milliseconds`,键按
  (route, method) 排序,标签值转义。框架不监听端口、不自动挂载 /metrics——端点与抓取策略
  归业务与网关
- 标签纪律:只传路由模板与标准方法(`MetricMethod`),不传完整 URL、用户 ID 或 trace_id

`_OTHER` 是记录器保留的归并标签，不应作为业务路由名称。正常 HTTP 路由模板以 `/` 开头；空/非法路由、未知方法及容量溢出按各自规则归并。记录器不承诺区分伪造的 `_OTHER` 输入与内部归并观测。

## OTLP 导出(可选绑定)

[telemetry/metric/otel](otel/)(包名 `metricotel`)把每次观测双写进 otel
`Float64Histogram`（`http.server.request.duration`，秒；`http.route` / `http.request.method` 属性）,复用既有 otel 依赖家族,不新增 require。Meter、
SDK 与 exporter 由业务构造和关闭(装配参考 [observability 示例](../../examples/observability/README.md),
示例同时演示本地直方图、`/metrics` 文本端点与 otel stdout 导出)。

Observe(ctx, Observation) 返回错误；nil context、负数、NaN、Inf 不计入本地或导出统计。
已经取消的 context 仍记录完成请求，且原样传给 OTel，保留追踪关联。空路由、超长路由、
控制字符与无效 UTF-8 归一为 _OTHER，非标准方法归一为 _OTHER。回调使用最终选定的键，
因此导出不会绕过本地容量限制。仍须传路由模板，框架无法从任意字符串推断路由参数。

DefaultBuckets() 每次返回独立切片；构造函数复制桶配置。OTel 默认使用 HTTP 语义约定的秒级桶；显式 Options.Buckets 仍以毫秒输入，导出时复制并转换为秒，
业务可通过 SDK View 覆盖导出聚合。Options.OnObserve 在本地记录后、锁外执行，
允许读取快照；回调由调用方保证并发安全。

## 资源占用（v0.1.3）

`metric.NewResourceRecorder(pools...)` 按 Snapshot/Prometheus 调用采样，不启动后台 goroutine，不连接数据库或 Redis。运行时提供当前堆字节、累计堆分配字节、GC 次数及 goroutine 数；Windows/Linux 提供当前进程常驻内存和累计用户态+内核态 CPU 秒。CPU 图表可用 `rate(process_cpu_seconds_total[1m])` 表示使用的 CPU 核数，不能直接解释为容器配额百分比。堆指标不等于进程 RSS。

```go
resources, err := metric.NewResourceRecorder(
    metric.SQLPool("database", db.SQLDB()),
    metricredis.Pool("cache", cache.Client),
)
if err != nil { return err }
// 在受保护的 /metrics 端点拼接 resources.Prometheus()。
```

`metricredis` 导入 `telemetry/metric/redis`；可选驱动适配不进入纯标准库的 metric 核心。SQL/Redis 输出连接总数、在用/空闲数、配置容量、累计等待次数和等待秒。Redis 配置容量是 PoolSize 目标，不是硬上限；不包含 PubSub/独立 pipeline 池，驱动 uint32 等待计数溢出时需按 counter reset 处理。数据库池为零上限时表示无限制。

自定义池用 PoolSource 的本地、并发安全 Snapshot 回调，值必须非负且有限，不执行网络请求。最多 32 个固定池名，每名 64 字符，仅字母、数字、点、下划线和连字符，禁止把用户或请求 ID 当池名。构造后声明不可变，返回快照互相独立。进程采样失败或其他平台不支持时，ProcessError 提供诊断、process_resources_available=0，CPU/RSS 指标省略，运行时和连接池仍可使用。资源当前只提供快照/Prometheus，现有 OTel 适配仍导出请求延迟。

此绑定对齐 OTel 指标名称、单位和路由/方法属性，不提供完整 HTTP 语义约定的 scheme/status 等属性。需要完整 HTTP 自动埋点时使用 telemetry/otel.Adapter.Handler；不要将两个记录器导出到同一 HTTP 指标流，避免重复统计。
