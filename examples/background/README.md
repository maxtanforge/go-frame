# 业务 worker 与调度装配

`worker.go` 是业务代码示例。Queue 接口位于示例包，由业务需要定义，不是 Frame 通用消息队列接口。业务适配具体 broker，实现 Receive/Ack/Nack；消息反序列化、幂等、重试延迟与死信归业务。

```go
budget, err := concurrency.NewExecutor(concurrency.ExecutorOptions{Concurrency: 4, RunTimeout: 5*time.Second})
if err != nil { return err }
worker, err := background.NewWorker(queue, budget, processMessage)
if err != nil { return err }
scheduler, err := schedule.New([]schedule.Task{
    {Name: "cleanup", IntervalMS: 60_000, TimeoutMS: 5_000, Run: cleanup},
    {Name: "daily", Cron: "0 3 * * *", Timezone: "Asia/Shanghai", Run: daily},
}, schedule.Options{OnEvent: func(e schedule.Event) {
    lg.Info("scheduled task", "task", e.Name, "state", e.State,
        "duration_ms", e.DurationMS, "error", e.Err)
}})
if err != nil { return err }
services, err := app.NewGroup(
    app.NamedService{Name: "worker", Service: worker},
    app.NamedService{Name: "schedule", Service: scheduler},
)
if err != nil { return err }
application, err := app.New(services, app.Options{Hooks: []app.Hook{
    {Name: "execution", Stop: budget.Shutdown},
}})
if err != nil { return err }
return application.Run(ctx)
```

实际接入将 queue 创建/探活/关闭放在 resource Hook，服务装配在依赖启动之后，参考 [basic](../basic/main.go)。Group 不拥有外部 queue，worker 的 Shutdown 完成后资源 Hook 才关闭客户端。同一 queue 是否允许多个消费者并发由驱动协议决定，不要把非并发 channel 任意共享。

需要启动后才取得 queue 时，先以 HTTP 等已有服务创建 Group，再在资源 Hook 后的
组合 Hook 创建 worker 并调用 `services.Add(app.NamedService{Name: "worker", Service: worker})`。
组合 Hook 的 Stop 返回 `services.Close()`，保证组合失败也清理已接纳的任务；Add 拒绝时
创建方负责关闭未被接纳的 worker。Run 或停机开始后不能再增加成员，无需业务维护 lazyGroup。

本例一次处理一条消息；取消后停止拉取，协作取消当前处理，再用独立、有限的 500 毫秒上下文 Ack/Nack。处理成功才 Ack；处理失败调用业务 Nack 策略；确认失败返回错误使关键 worker 退出，由 app 排空服务。业务 panic 由 app.Task 上报，未确认消息的重新投递由 broker 管理。这里只演示协作取消，不承诺停机后继续完成任意长任务。App 退出预算需容纳确认和资源清理，必要时调整业务确认预算。

停机时，处理器直接返回 `ctx.Err()` 或通过单链 `%w` 包装的纯取消属于正常退出。若返回 `errors.Join(ctx.Err(), businessErr)`，即使 Nack 成功，Run/Shutdown 仍保留业务失败；确认失败也与处理失败合并，调用方可用 `errors.Is` 检查各个原因。运行期间处理失败在 Nack 成功后记录日志并继续消费。

## 多副本互斥

在定时任务的 Run 内显式获取锁，必须在工作前注册 defer 释放，确保 panic 时也会清理：

```go
func runExclusive(ctx context.Context) (err error) {
    held, err := locks.Acquire(ctx, "daily", false)
    if errors.Is(err, redislock.ErrContended) { return nil }
    if err != nil { return err }
    defer func() {
        cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
        defer cancel()
        err = errors.Join(err, held.Release(cleanup))
    }()
    return errors.Join(daily(held.Context()), held.Err())
}
```

`locks` 是业务入口使用共享 Redis 客户端创建的 `lock/redis.Locker`；别名 `redislock` 避免与驱动冲突。任务中竞争失败直接返回 nil 是业务“跳过”策略，scheduler 会记录成功，若需单独统计分布式竞争跳过，应在这个分支上报业务事件。内置 Skipped 只表示本地同任务忙碌。

租约仅限制同时执行的窗口；先后获取锁仍可能重复处理同一周期。需要每日唯一处理时，业务使用日期幂等键和持久化唯一约束。Frame 不存储任务历史、不做补跑或消息重试。

运行 `go test ./examples/background` 验证消息确认与停机时取消/确认顺序，无真实消息队列。
