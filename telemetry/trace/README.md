# 请求关联 ID

`telemetry/trace` 是 traceId 的单一事实源:请求日志(RequestLog)与响应包络(respond)共用
`FromContext(ctx)` 的同一个值,响应头统一回写 `x-trace-id`。

```go
r.Use(trace.Middleware()) // 默认进程共享雪花生成器，节点 0
r.Use(trace.Middleware(trace.WithSnowflake(sf), trace.WithGenerateError(func(err error) { ... })))
```

- 上游可带 `x-trace-id`;`ValidID` 限定 1–128 个 ASCII 字母、数字与 `._-`,缺失或非法时替换
  为生成值,响应头始终回写最终值。
- 默认使用进程共享的节点 0 雪花生成器，输出十进制字符串。`WithSnowflake` 注入业务服务
  的生成器，请求关联 ID 和业务 ID 可共用同一个生成器。nil（含类型化 nil）使用默认生成器。
  多实例部署通过 `ID_NODE` 分配不同节点号（0–1023）；服务模板默认节点 0。
- 生成失败报告给 `WithGenerateError` 观察者，并返回通用 500，不执行业务处理，也不切换
  节点或 ID 算法。观察者在请求路径上执行，必须廉价。
- traceId 是请求关联标识:不是 W3C traceparent,不承载采样/父子关系,更不是可信身份——
  有效性校验不等于信任。分布式追踪经 `router.Options.Middleware` 接入 otel。
- 指标与日志不得把 traceId 当标签使用;它只随日志字段与响应包络出现。
