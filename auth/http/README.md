# HTTP 认证与公开接口

`auth/http` 接受 `Verifier` 接口，不依赖具体 JWT 实现。`token.Verifier` 可以直接传入。
验证失败通过 `principal.ErrUnauthenticated` 识别；普通后端错误返回 500，超时保持 504。
可选 `Options.Check` 在验证成功并发布已验证身份后检查账号／会话状态；检查失败不会进入业务处理，但日志仍保留实际操作人。

```go
guard, err := auth.New(verifier, auth.Options{
    PublicEndpoints: []auth.Endpoint{{Method: "POST", Path: "/auth/login"}},
})
// 先检查 err，再将 guard.Middleware 挂到业务路由入口。
```

`Middleware` 默认保护全部请求，名单为空也不会关闭认证。名单按大写方法与精确路径
匹配；尾斜杠不同、HEAD 不继承 GET、OPTIONS 不自动放行。不支持通配符、参数路径、
query 或编码别名。query 不参与请求匹配，非规范／RawPath 请求不获得公开豁免。
公开接口忽略 Authorization，不把过期 token 当作登录失败，也不从 token 取得操作人。
`Require` 总是验证，`Optional` 仅允许完全没有 Authorization 的请求匿名通过。

路由适配器可在实际匹配路由后使用 `EndpointMiddleware(endpoint)`，按可信路由的
方法和路径判断策略，适用于挂载前缀；原始 URL 不被改写，仍检查规范路径和 RawPath。
不要将公开 endpoint 应用到整个路由器，也不要从客户端 Header 获取 endpoint。
业务使用 chi 时优先由 `router.NewAuthenticated` 完成这层装配。

凭证只来自单个 `Authorization: Bearer ...`，重复／空／畸形头返回 401，响应包含
`WWW-Authenticate: Bearer`。不读取 Cookie、URL 参数或私有头。公开并不免除登录凭据、
Webhook 签名等业务校验。

认证会设置 `auth/principal` context 值，并派生带 actor_id/actor_name/actor_issuer 的请求 logger。
外层 AccessLog 通过框架内部的请求观测状态取得身份；不是依赖 context 向外传播。
业务使用 `principal.Require(ctx)` 或 `FromContext`，不信任客户端 X-User-ID。
`database/audit/principal.FromContext` 可取得 audit.Actor；缺少身份不回退为系统用户。

想把公开声明和路由写在一起，使用可选 `http/router.NewAuthenticated`：

```go
r, err := router.NewAuthenticated(lg, router.AuthenticatedOptions{
    Verifier: verifier,
    Operations: recorder,
    Routes: []router.Route{
        {Method: "POST", Path: "/auth/login", Public: true, Handler: loginHandler},
        {Method: "GET", Path: "/me", Handler: meHandler,
            Operation: operationhttp.Metadata{Action: "profile.read", ResourceType: "profile"}},
    },
})
```

组合入口验证重复声明、方法大小写、公开路径和 handler。`Policies()` 是独立副本，
列出构造时的路由策略（包括健康端点）；后续通过 chi 添加的新 method/path 默认受保护，
但不进入启动清单。不能覆盖已声明的路由，服务启动后禁止变更路由。
内置健康端点默认也受保护，需要时通过 `PublicEndpoints` 分别公开 GET／HEAD。
应用自定义中间件不得在通过认证后将请求重写到另一个资源，或把未验证身份发布到 context。

参见 [完整接线示例](../../examples/authenticated/README.md) 和 [令牌协议](../token/README.md)。
