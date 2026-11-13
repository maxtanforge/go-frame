# HTTP router

For typed business handlers, use `Route{Method, Path, Public, Operation}.Bind(...)`
with the `(http.Handler, error)` returned by binding, then call
`Collect(module, declarations...)`. Collection checks declarations before
returning a route list; a failure returns no list and retains the original cause
with module/method/path context. Feed the list into `NewAuthenticated` at startup.
See the [business example](../../examples/transactions/README.md).
`Bind` rejects a route whose `Handler` is already set. `Collect` and
`NewAuthenticated` reject exact method/path duplicates and declarations that
overwrite an equivalent chi pattern, such as `GET /items/{id}` and
`GET /items/{name}`. The startup check also covers routes combined from multiple
modules. Different methods, distinct regular expressions and static/parameter
overlaps retain chi's matching rules; the check does not prove that arbitrary
regular expressions are disjoint. Built-in health-route conflicts are checked by
`NewAuthenticated`. Direct registration on `New` follows chi's own behavior.

`router.New(logger, router.Options{})` returns a chi router with trace IDs,
access logging, panic recovery, cooperative request deadlines, and `/healthz`
and `/readyz` endpoints. Register routes before starting the server and keep
the route tree immutable while serving.

Recovery logs use the supplied logger even if trace generation or its error
observer panics before access logging starts. These early failures have no
generated trace ID, access completion event, or `OnRequest` callback. The panic
cause and stack are recorded once; `http.ErrAbortHandler` remains a transport
abort and is rethrown without a second panic log.

```go
import (
    "net/http"

    "github.com/maxtanforge/go-frame/http/server"
    "github.com/maxtanforge/go-frame/http/router"
)

r := router.New(nil, router.Options{})
r.Get("/example", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNoContent)
})
srv := server.New("127.0.0.1:8080", r)
```

`Timeout` defaults to 30 seconds; a negative value disables it for streaming.
Handlers must honor their request context. Supply `Health` for dependency
readiness checks; without a checker, both `/healthz` and `/readyz` fail closed
with 503 rather than reporting a fabricated healthy state. `DisableHealthMount`
skips both endpoints when external probing owns liveness; `NewAuthenticated`
then also omits them from its startup catalog. `OnRequest` observes
completion synchronously and must support
concurrent calls; `QuietHealth` suppresses successful health access logs without
suppressing metrics. `Middleware` appends application middleware, while
authentication should be attached to the appropriate chi route groups.

The router does not trust forwarded IP headers. It renders framework error
envelopes for unmatched routes, unsupported methods and recoverable handler
panics. A 405 response includes allowed methods for the effective routing path,
including path normalization and mounted chi subrouters. Normalization middleware
must itself support its placement in the route tree; the router does not replay
middleware when computing allowed methods.
After a response has been committed, a panic aborts the transport rather
than appending an error envelope to a partial stream.

This package owns HTTP composition; `http/server` owns the listener, TLS and shutdown.

Choose one framework router per request path: `NewAuthenticated` already includes
`New`'s trace, access log, recovery, deadline and health boundary. Supply all shared
settings through `AuthenticatedOptions.Router`, including Timeout, Health, Trace
and OnRequest. Mounting it inside another `router.New` repeats these wrappers:
the shorter deadline wins, two access events are emitted and trace IDs may differ.
Use a plain `chi.NewRouter()` parent only when a mount prefix is needed. When auth
depends on started resources, build the complete authenticated router in an app
composition Hook and register its HTTP service with `Group.Add` before Run.

`NewAuthenticated` can be mounted under a chi parent. Declare policies relative
to this router; authentication and operation metadata use its actual matched
route, while handlers retain the original URL. Unmatched routes and newly added
routes remain protected. Encoded or noncanonical URLs never receive a public
exemption. Application middleware must not rewrite the target after authentication.
`NewAuthenticated` protects built-in health routes by default and warns at startup
when any GET/HEAD probe remains protected. For unauthenticated infrastructure
probes, explicitly list all required method/path pairs in `PublicEndpoints`;
otherwise supply credentials or use `DisableHealthMount` with external probing.
GET and HEAD are separate policy entries.
Applications using another router can depend on `http/server` directly without
importing chi or framework response middleware.

Trace generator typed-nil values are treated as absent and use the default Snowflake generator.
The outer recovery boundary covers trace generator/error-observer panics before
business handling and returns a generic 500; no internal panic text is exposed.
A failure before trace initialization may have no trace ID or access-log event.
Inner recovery remains inside the deadline middleware so handler timeouts and
already-committed response aborts retain their existing behavior. Standalone
`trace.Middleware` consumers must provide their own recovery boundary.

v0.1.3支持 `AuthenticatedOptions.Middleware`、`Groups[].Middleware`
和 `Route.Middleware` 三层声明。`Group{Name, Routes}` 的路径显式填写；分组不会增加前缀。
每条有效链按应用、分组、路由的声明顺序执行，同阶段可用 `Before/After` 引用名称调整顺序。
重名、缺失引用、跨阶段引用、循环依赖、nil wrapper/返回值在启动时失败。
wrapper 构造时发生 panic 也会阻止启动，错误标明中间件名称；error 类型的 panic 保留原始原因，
可用 errors.Is/As 检查，其他 panic 值不写入错误文本。声明路由的构造错误同时保留方法和路径。
chi 在首次挂载路由或调用 With/Group 等触发链构造的操作时构造中间件链。
若关闭内置健康路由且初始声明为空，构造会延后到这些操作；
这时失败通过携带构造错误的 panic 终止当前操作，调用方应中止启动，不应继续使用该路由器。

普通认证服务推荐由 feature 返回 Route.Bind/Collect 声明，bootstrap 汇总后通过
NewAuthenticated 的 Routes/Groups 一次编译，在开始服务前完成注册。这样公开策略、
操作元数据、命名阶段和启动目录共享同一份声明。直接补注册与原生 With/Group/Mount
保留给需要自行组合的入口；后补路由不进入启动目录，完整阶段能力以声明路由为准。

执行顺序为 `Response → Matched → 认证与 Check → AuthenticatedPhase → BeforeHandler → handler`，
返回顺序相反。公开接口也执行认证后阶段，但可能没有 Principal；业务应明确身份要求。
`router.Policy(ctx)` 返回匹配策略的独立副本。声明路由的 `BeforeHandler` 可以读取 chi params，
并包围 DTO 绑定、校验和响应输出。内置健康及后续直接注册路由仅应用全局阶段，
其 BeforeHandler 位于 chi 分发前；需要完整阶段能力的业务路由应通过 Routes/Groups 声明。
未匹配请求不执行这些命名阶段；需要观察所有请求时使用原有 `Router.Middleware`。

`Response` 包围匹配路由的认证失败、业务失败及可恢复 panic，可设置响应头并在 next 返回后收尾。
它不自动缓存或改写响应正文，文件下载、SSE、流式响应保留原有协议。自定义 ResponseWriter
包装器应提供 `Unwrap() http.ResponseWriter`，或正确保留需要的可选接口。
修改 DTO 返回值使用 [binding.Intercept](../binding/README.md)，在序列化前执行。
外层中间件不能通过 context 反向取得内层新增 Principal；框架完成日志通过请求观测状态保留身份。

匹配后的中间件不得修改 Method、URL.Path、URL.RawPath 或 chi RoutePath 再调用 next；
框架在分发及声明 handler 前检查，违规则终止执行并返回错误。
301/302 等重定向照常通过 `http.Redirect(w, r, location, status)` 返回，不再调用 next，
无需改写请求路径。确需路径规范化时，在挂载父路由、进入认证路由前完成。
