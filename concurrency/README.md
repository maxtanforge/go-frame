# Per-process concurrency tools

v0.1.3提供共享执行预算 `NewExecutor(ExecutorOptions)`：

```go
budget, err := concurrency.NewExecutor(concurrency.ExecutorOptions{
    Concurrency: 0, // 启动时快照 GOMAXPROCS，适合 CPU 工作
    MaxWaiters: 16, WaitTimeout: 100*time.Millisecond,
    RunTimeout: 5*time.Second,
})
if err != nil { return err }
err = budget.Do(ctx, func(ctx context.Context) error { return process(ctx) })
```

`Do` 在调用方 goroutine 执行，不另起后台 worker，也不调整全局 GOMAXPROCS。
I/O 任务应显式声明适合连接池的 Concurrency。等待数默认 0，过载返回 ErrOverloaded；
开启等待必须声明正的 WaitTimeout，RunTimeout 从准入后开始，0 表示沿用调用方预算。
回调必须响应 context；超时不能强杀 goroutine。禁止在同一个已占用预算中无限嵌套 Do。
Stats 暴露运行、等待和累计拒绝情况，不能作为容量预留。

在资源 Hook 中注册 Stop：`Shutdown(ctx)` 关闭准入、取消等待者并排空已运行工作，
停机预算耗尽时取消运行 context；必要时 `Close()` 立即取消全部工作。
预算对象可供 HTTP 和后台任务共享，也可在启动时按业务声明多个独立预算。
HTTP 把 ErrOverloaded 映射为显式过载错误；队列通过已有 Ack/Nack 策略处理，不自动重试。

`Map` runs a finite batch using at most the configured number of workers and
returns results in input order:

```go
results, err := concurrency.Map(ctx, ids, 8,
    func(ctx context.Context, id string) (Item, error) {
        return repository.Load(ctx, id)
    })
```

The first callback error cancels sibling work. All started callbacks finish
before Map returns; errors return nil results rather than a partial batch.
Parent cancellation takes precedence over callback errors. Empty input returns
a nonnil empty slice after validation. Worker count must be positive, context
and callback nonnil. There is no goroutine per input. Callbacks must honor the
provided context and must not detach work; panics follow normal Go behavior.

`NewLimiter(capacity)` bounds simultaneous admissions to an expensive operation:

```go
release, err := limiter.Acquire(ctx)
if err != nil { return err }
defer release()
return callUpstream(ctx)
```

`TryAcquire()` admits immediately or returns false, useful when a caller wants
to reject overload rather than wait. Release is safe to call repeatedly and
concurrently. `InUse()` reports a snapshot, not a reservation. A canceled context
before Acquire cannot consume capacity; cancellation racing admission can return
success or an error without leaking a slot. Always release successful admission.

`NewBoundedLimiter(capacity, maxWaiters)` additionally bounds waiting callers:

```go
limiter, err := concurrency.NewBoundedLimiter(32, 16)
if err != nil { return err }
waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
release, err := limiter.Acquire(waitCtx)
cancel()
if err != nil { return err }
defer release()
return callUpstream(ctx) // keep the operation's original context
```

When execution and waiting capacity are full, Acquire returns `ErrOverloaded`;
use `errors.Is` to classify it. Zero maxWaiters rejects immediately. Negative
maxWaiters is invalid. Cancellation or deadline expiry releases waiting capacity;
waiting duration is controlled by the caller's context. Admission is not FIFO.
`Stats()` reports Capacity, MaxWaiters, InUse, Waiting and cumulative Rejected;
fields are independent snapshots, not reservations. Rejected counts capacity
rejections from Acquire/TryAcquire, excluding cancellations and deadline expiry.
For the original unbounded NewLimiter, MaxWaiters is -1 and Waiting is not tracked.

The limiter creates no internal goroutines. Acquire waiters are caller-owned
goroutines. The original NewLimiter leaves their count unbounded; choose
NewBoundedLimiter for an explicit waiting limit or TryAcquire for immediate
admission. HTTP callers can compose [Admission](../http/middleware/README.md#admission).
The limit does not cap connections or goroutines already created by HTTP servers.
These tools are not rate limiters, distributed quotas, task schedulers or retry
engines. A callback that ignores context can prevent its batch from finishing.
