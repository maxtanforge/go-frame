# Application services and tasks

`App` compiles resource-hook dependencies before startup, then runs its service. On
cancellation or service exit, it marks readiness down through `OnShutdown`, drains
the service, and stops resources in reverse compiled order. Shutdown uses a fresh budget.

v0.1.3提供 `Hook.DependsOn`。模块/组合 Hook 显式引用资源 Hook 名称，
`app.New` 在任何 Start 前检查缺失、自依赖、重复依赖和循环依赖；同等就绪的 Hook
保持声明顺序。声明最多 4096 个，名称最长 128 字节，依赖表在构造时复制。
这只描述启动成功依赖：服务入口仍由 Group 显式组合，Group 的并行排空不会自动推断
依赖停止顺序。资源在服务排空后释放；启动失败连同部分初始化的失败 Hook 反向回滚。

```go
app.Options{Hooks: []app.Hook{
    {Name: "compose", DependsOn: []string{"database"}, Start: compose},
    databaseResource.Hook(),
}}
```

`ShutdownTimeout` covers readiness withdrawal, service drain, waiting for the
service to return, and resource cleanup together. Set `CleanupReserve` to retain
time for resource Stop hooks even when draining exhausts its budget:

```go
app.Options{
    ShutdownTimeout: 15 * time.Second,
    ShutdownDelay:   2 * time.Second,
    CleanupReserve:  3 * time.Second,
}
```

This allows withdrawal and drain through second 12, while Stop hooks share the
original deadline at second 15. If drain finishes sooner, hooks can use all the
remaining time. `ShutdownDelay + CleanupReserve` must be less than the total
timeout; both must be nonnegative. A zero reserve preserves the shared-budget
behavior, so a timed-out drain can leave Stop hooks with an expired context.
Startup rollback always receives the full fresh `ShutdownTimeout`, regardless
of the reserve. Callbacks must honor contexts and Close must return promptly:
these deadlines cannot forcibly terminate uncooperative callbacks or goroutines.

Use a `Group` to compose several critical services and context-aware background
jobs under the same resource lifetime:

```go
worker, err := app.NewTask(func(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case item, ok := <-jobs:
            if !ok { return nil }
            if err := process(ctx, item); err != nil {
                return err
            }
        }
    }
})
if err != nil {
    return err
}
services, err := app.NewGroup(
    app.NamedService{Name: "http", Service: server},
    app.NamedService{Name: "worker", Service: worker},
)
if err != nil {
    return err
}
application, err := app.New(services, app.Options{Hooks: resourceHooks})
if err != nil {
    return err
}
return application.Run(ctx)
```

`Group.Add` registers additional services during startup composition, after
resource hooks have supplied their handles. Registration freezes atomically when
Run, Shutdown or Close begins. A batch with an invalid/duplicate name or nil
service adds nothing; the caller still owns rejected services. When a Group is
passed directly to App, startup rollback closes its accepted services before
stopping any hooks. The composition Hook's Stop owns its other local cleanup.

升级时，若组合 Hook 的 Stop 已有 `group.Close()`，请移除该调用并保留其他清理。
App 直接管理 Group 时已负责启动回滚，重复调用会再次报告缓存的关闭错误。

App 管理 Group 时，成员排空失败由 Group 执行强制关闭。排空预算耗尽时，Group 只关闭尚未完成排空的成员。
成员的运行、排空和关闭错误一并返回。直接调用 Group.Shutdown 仍可获得成员运行诊断，
直接调用 Task.Shutdown 仍可获得任务回调错误。

In v0.2.1, `NewGroup()` may initially be empty. Construct HTTP and
workers inside the composition Hook once their dependencies are available, then
Add them before returning. Run on a still-empty group fails immediately and
leaves Shutdown/Close safe to call. This removes the need for a custom lazy
service wrapper or a preconstructed outer HTTP router.

`app.Cleanup` (v0.2.1) owns resources acquired dynamically inside composition.
Use a separate instance for each composition Hook and attach Stop before startup:

```go
var cleanup app.Cleanup
compose := app.Hook{
    Name: "compose", DependsOn: []string{"database"},
    Start: func(ctx context.Context) error {
        client, err := openClient(ctx)
        if err != nil { return err }
        if err := cleanup.Add("client", func(context.Context) error {
            return client.Close()
        }); err != nil {
            return errors.Join(err, client.Close())
        }
        return composeServices(ctx, client, services)
    },
    Stop: cleanup.Stop,
}
```

Stop freezes registration and invokes callbacks once in reverse order. Each
error or panic retains its resource name; failures do not skip other callbacks.
The first caller executes cleanup synchronously with its context. Concurrent
callers may stop waiting using their own budgets, and completed calls reuse the
cached result. Even an expired context is passed to every callback so immediate
release remains possible. Callbacks must cooperate with the budget and must not
recursively call Stop on the same Cleanup. A rejected Add leaves ownership with
the caller. Cleanup is not a resource registry; continue injecting concrete
dependencies. Resources that need their own startup dependencies or health probes
still use individual `app/resource` hooks.

Every member is critical: an early return, even nil, makes Group.Run return so
App begins draining siblings. Group.Run does not itself stop siblings; standalone
callers must call Shutdown and, on timeout or failure, Close. Child Run, Shutdown and Close panics
become named errors. Shutdown calls every member concurrently with the identical
context and waits for child Run calls within that budget. Errors from
Run, Shutdown and Close retain member names and support `errors.Is`.
若普通成员的 Shutdown 失败，Group 立即关闭该成员，其他成员继续排空。嵌套 Group
沿用相同的排空预算，超时后只强制关闭仍未排空的成员。这些关闭操作共享 Group.Close
的至多一次关闭记录。

Group and Task are one-shot and safe for concurrent stop calls. Stopping before
Run prevents startup. The first Group.Shutdown call owns the child shutdown
context; subsequent calls wait within their own contexts without restarting
shutdown callbacks. Group.Close calls every child Close at most once and caches
its errors. It does not wait for child Run completion.

Task owns a background context, canceled by Shutdown or Close. Shutdown waits
within its caller's budget; Close only requests cancellation. A callback's direct
`context.Canceled` return after cancellation is normal completion; other errors
and panics are retained. Callbacks must cooperate with cancellation, and service
Close must promptly unblock Run. Child services must also tolerate Shutdown or
Close racing their Run entry. A timeout cannot forcibly terminate a goroutine;
uncooperative work may outlive resource cleanup. There are no automatic restarts,
dependency graphs, signal handlers, or process exits.

Recovered App/Group Run/Shutdown/Close, Task, Hook Start/Stop, and App OnShutdown panics retain the original value and panic-site
stack in `*app.PanicError`. The error chain preserves an error-valued panic for
`errors.Is`. Read diagnostics explicitly; the normal error string omits the stack:

```go
var panicErr *app.PanicError
if errors.As(err, &panicErr) {
    log.Error("background service panicked", "error", err, "stack", string(panicErr.Stack()))
}
```

`Stack()` returns a defensive copy. An OnShutdown panic becomes a named error while
service draining and reverse resource cleanup continue; their failures remain joined.
In v0.2.1, service Shutdown panics follow the same path as returned failures:
App attempts Close and continues reverse resource cleanup; Group closes the failed
member while siblings continue draining. Close panics also retain diagnostics,
and Group caches them with its at-most-once close result. Run, drain, close and
resource cleanup errors remain joined. If a failed Close leaves Run blocked,
waiting still ends at the drain deadline.
Recovery covers only the callback invocation, including callbacks invoked by
Group's worker goroutines. Goroutines created inside a callback retain ordinary
Go panic behavior. Recovery does not restart work or replace application error handling.

Hook Start panics follow the same rollback path as returned errors, including
the partially initialized hook. Stop panics become named errors while reverse
cleanup continues; all startup and cleanup errors remain joined.

Group shutdown is concurrent, not dependency-ordered. If an in-flight HTTP
request needs a worker to finish, that worker must drain its accepted work on
cancellation, or the application must provide a service adapter with explicit
stop ordering. A Group does not infer those relationships.

Task 停机后仅将直接取消或单条 `Unwrap() error` 链最终指向
`context.Canceled` 的错误视为正常完成。复合错误（包括只有取消的 errors.Join）
和 `PanicError` 始终保留，避免吞掉清理失败与 panic 栈；自定义 Is 匹配不用于此归一化。
