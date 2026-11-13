# HTTP 健康端点

`healthhttp.New(checker)` 借用 `health.Checker`，提供 GET/HEAD `/healthz` 和 `/readyz`。
核心探测通过 `checker.Readiness(ctx)` 返回独立报告，HTTP 适配负责日志、响应包络与状态码。
必需依赖失败返回 503；仅可选依赖失败返回 degraded/200；排空时 readiness 返回 503，
liveness 仍可成功。请求取消和超时继续采用 respond 的共同策略。

`http/router` 已使用这个适配。启用认证后是否公开健康端点，仍由业务白名单显式决定。
报告中的失败原因不输出 JSON，HTTP 适配仅在诊断日志里记录；不要把完整报告当作业务响应。

需要路由前缀或自定义路径时，将 `NewLiveness(checker)`、`NewReadiness(checker)`
分别绑定到存活／就绪路由；它们不按完整 URL 二次匹配。框架 router 已采用这种接线，
支持嵌套 Mount 前缀。独立的 `New(checker)` 仍严格匹配 `/healthz` 和 `/readyz`，
使用标准库挂载前缀时需显式 StripPrefix。三个入口均只接受 GET/HEAD，nil checker 返回 503。
