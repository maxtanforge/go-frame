# 本地调度

`schedule.New` 接收固定任务列表并复制注册信息，返回实现 `app.Service` 的一次性调度器。任务必须有唯一非空名称、回调，以及二选一的固定间隔或 Cron。构造完成后不提供动态注册/删除；重启需要创建新调度器。

```go
scheduler, err := schedule.New([]schedule.Task{
    {Name: "cleanup", IntervalMS: 60_000, TimeoutMS: 5_000, Run: cleanup},
    {Name: "daily", Cron: "0 3 * * *", Timezone: "Asia/Shanghai", TimeoutMS: 30_000, Run: daily},
}, schedule.Options{OnEvent: observe})
if err != nil { return err }
application, err := app.New(scheduler, app.Options{})
if err != nil { return err }
return application.Run(ctx)
```

所有数值时间配置均为毫秒，负数和转换为 `time.Duration` 会溢出的数值被拒绝。`TimeoutMS: 0` 表示无单次超时。间隔首次触发在 `Run` 后一个完整周期，后续从实际调度时刻计算下一次间隔，不按任务结束时间计算。Cron 使用 `robfig/cron/v3` 的标准五字段格式：分、时、日、月、周；支持它的范围、列表、步长和命名月份/星期。不支持秒字段、`@every` 等描述符和表达式内时区；`Timezone` 必须显式使用 UTC 或 IANA 名称，不接受依赖机器配置的 `Local`。内置 Go 时区数据，时区夏令时行为遵循解析器。

同一任务最多一个执行，忙碌时触发产生 `Skipped`，不排队，不补跑，不立即重试。调度循环延迟时只处理一次已到期触发，从当前时间求下一次，不逐个重放历史周期。不同任务可以并行；最多每个注册任务一个工作 goroutine。每个副本各自调度，内存内不重叠不代表跨副本唯一执行。

`Shutdown(ctx)` 先停止触发，取消在途任务，再在 ctx 预算内等待。等待不创建后台 waiter goroutine；超时返回 `ctx.Err()`，后续可再次调用等待。`Close()` 只请求取消，立即返回；`Run` 在调度循环退出时返回，在途任务由 `Shutdown` 等待。已停止的调度器不会重新启动。回调须主动响应 context；Go 无法强制终止忽略取消的业务代码，超时后它仍占据该任务槽位，直到实际返回。不要在任务回调或事件观察器中同步调用 `Shutdown` 等待自身。

事件含任务名、`Started` / `Succeeded` / `Failed` / `Canceled` / `Skipped`、终态耗时 `DurationMS` 和 `Err`。正常关停取消的任务返回 nil、直接 `context.Canceled` 或单条包装链取消时产生 `Canceled`（DEBUG）事件。独立取消、单次超时、复合错误（含 errors.Join）和 panic 仍为失败；panic 使用 `PanicError` 保留原始值和堆栈，可通过 `errors.As` 读取。任务失败不结束调度器。`OnEvent` 可以并发调用，必须快速返回；观察器 panic 被捕获并写入 slog，不能阻断其他任务。观察器和业务回调捕获的外部状态仍由业务管理；构造函数只复制注册值。未提供观察器时使用 `slog.Default`，失败为 ERROR，其他事件为 DEBUG。错误、panic 值和堆栈可能包含业务信息，应使用受信任日志目标。

多副本需要互斥时，在任务的 `Run(ctx)` 中组合 `lock/redis` 租约适配，获取失败按业务规则返回/跳过，使用租约工作 context 并在结束时释放。Redis 故障切换、租约到期和进程暂停仍可能造成并发；这不承诺每周期只执行一次或 exactly-once。业务必须使用幂等键、数据库唯一约束或业务侧 fencing 保护写入。队列消费循环使用 `app.Task`/`app.Group`；消息重试、确认与死信策略由业务拥有。
