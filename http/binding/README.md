# 强类型 HTTP 绑定

新增入口：`Form` 处理 URL-encoded 表单；`PrepareMultipart[T]` 返回可复用解码器，每请求传入文件接收回调；`PrepareXML[T]` / `XML` 组合原生 XML 解码和值校验。详见 [表单上传](../request/FORM.md) 与 [XML](../request/XML.md)。XML 成功响应不套 JSON 包络。

binding 在注册时组合 request.Prepare、tags.Prepare 与 respond.Adapter。业务处理函数使用 `func(context.Context, *Req) (*Res, error)`，无须接收 ResponseWriter，也不用逐请求编译声明。

```go
type ListReq struct {
    Current int64 `d:"1" v:"gte=1"`
    Size int64 `d:"20" v:"gte=1,lte=100"`
}
func List(ctx context.Context, req *ListReq) (*ListRes, error) {
    return service.List(ctx, req.Current, req.Size)
}

handler, err := binding.Query(List)
if err != nil { return err } // 声明错误必须阻止启动
r.Method(http.MethodGet, "/products", handler)
```

Query、JSON、Path 都返回 (http.Handler, error)，请求通过才调用业务函数；返回值沿用 respond 包络。nil 处理函数、无效 DTO/规则、缺少 PathParams 都在注册时失败。无内容接口可以使用显式空结果类型并返回 nil 结果；状态码不根据返回类型自动猜测。

Options 最多传一个，Limits 配置 request.Options，Validation 配置 tags.Options，PathParams 显式提取已解码的路径参数：

`Options.MapError func(error) error` 可集中配置模块的业务错误转换，适用于 Query、JSON、Path、Form、JSONWithPath 与 PathWithQuery。只转换业务回调的非 nil 错误，不转换解码、校验、注册期或成功响应的序列化错误。包含 context.Canceled / context.DeadlineExceeded 的错误、请求 context 已结束时直接保留。mapper 返回 nil 仍保留原始失败；映射结果未保留原始 cause 时，以 errors.Join(mapped, original) 补足，映射错误优先，errors.Is 仍可匹配原始错误。每次最多调用一次，不自动重试。选项在构造 handler 时捕获；mapper 跨请求共享，应用负责并发安全。未设置 MapError 时保持已有行为。

mapper 对子操作取消/超时的跳过策略仍然适用；HTTP 504 只由请求 context 的实际 deadline 到期触发。请求仍有效时，子操作超时按显式 httperror.Error 分类，否则为 500。自定义校验中的取消/超时是终止错误，即使与字段集合合并也不公开其中的诊断；原始 cause 保留供日志及 errors.Is 使用。显式公开错误、ErrSchema 和 ErrLimit 保留各自分类。

```go
handler, err := binding.JSONWithPath(
    func(ctx context.Context, path *ProductPath, body *PatchProduct) (*ProductView, error) {
        return service.Patch(ctx, path.ID, body)
    },
    binding.Options{PathParams: func(r *http.Request) map[string]string {
        return map[string]string{"id": chi.URLParam(r, "id")}
    }},
)
if err != nil { return err }
r.Method(http.MethodPatch, "/products/{id}", handler)
```

JSONWithPath 使用独立 Path/Body DTO，先解析路径再解析 JSON，不建立跨来源覆盖规则。可收集的字段错误按路径、请求体顺序合并；语法、资源限制和内部错误会终止。PathParams 同样是 Path 的必需选项。底层不依赖 chi，其他路由器只需提供同样的提取函数。

标签使用 d/v/label，默认名称为 snake_case；present 与 required 的差别、PATCH nullable 和转换规则见 [request](../request/README.md)。条件校验使用 [validation.Context](../../validation/README.md)，值规则由 [tags](../../validation/tags/README.md) 执行。binding 合并绑定和校验的完整集合，HTTP 只展示首条安全消息、data 为 null；错误不通过时业务函数不执行。

当前路由由应用显式注册，binding 不扫描控制器、方法或 Meta。返回的 handler 可直接放入 router.Route.Handler；认证公开名单、操作日志和权限策略继续由路由/服务层管理。DTO 不会自动映射到 Ent Set/Clear，不自动开放系统字段。普通入口通过 Route.Bind/Collect 保留 handler 构造错误，由 bootstrap 汇总声明；框架不提供控制器扫描或接口 Meta DSL。

普通业务入口的完整有类型回调、路由表和规范 ID 适配见[业务接入示例](../../docs/business-integration.md#普通业务入口有类型的处理函数与显式路由)。binding 已执行字段校验，回调不再重复 Validate；Service 仍需保护权限和非 HTTP 调用的业务不变量。

PathWithQuery[P,Q,R] 组合独立 Path/Query DTO，注册时预编译两套声明，先处理路径再处理查询。可收集的问题按路径、查询顺序合并；硬错误停止后续解析，只有全部通过才执行回调。两个来源的默认值和输入状态独立，每个请求使用新 DTO。用法见 [frame 消费示例](../../examples/transactions/README.md)。JSONWithPath 会先完成输入处理再调用业务函数；需要在正文失败前设置父资源审计信息的入口仍保留显式适配。

binding 使用声明式字段绑定，不保证执行 DTO 的自定义 UnmarshalJSON。迁移旧 JSON 入口时，检查其中的协议规则、业务错误码和失败顺序；可使用已有 Validate(*validation.Context) 表达字段与跨字段检查，授权与业务状态仍归 Service。原始标量格式可以先保留为 string，再在自定义校验中验证，不必立即增加框架规则。

按协议语义选择入口：

| 场景 | 推荐入口 | 业务负责 |
| --- | --- | --- |
| 普通 JSON、query、path DTO | binding.JSON/Query/Path 及双来源组合 | DTO 声明、业务输入转换与业务不变量 |
| 依赖自定义 UnmarshalJSON 的 JSON 协议 | request.DecodeJSON + respond.Adapter | 使用其 Validate() error 契约或显式校验；不自动获得 Prepared 默认值与字段状态 |
| 正文解析前必须检查父资源或设置审计上下文 | Prepared/Adapter 显式编排 | 输入处理顺序、授权与错误映射 |
| 文件、SSE 等直接写出 | 原生 handler 或 respond.Adapter 中写出后返回 nil, nil | 流式协议、资源释放与取消 |

v0.1.3提供 `Intercept[Req, Res]` 和 `InterceptPair[Path, Input, Res]`。
拦截器接收强类型 next，在注册时组合，按声明顺序进入、逆序返回；输入解码和校验失败时不会调用。
可统一处理业务权限、结果转换和错误，返回结果仍由 binding/respond 序列化；不读取或缓存原始响应正文。
两来源版本用于 JSONWithPath/PathWithQuery；所有回调共享使用，必须支持并发请求。

```go
callback, err := binding.Intercept(List, func(next binding.Handler[ListReq, ListRes]) binding.Handler[ListReq, ListRes] {
    return func(ctx context.Context, req *ListReq) (*ListRes, error) {
        result, err := next(ctx, req)
        return result, err // 可在此统一处理强类型结果
    }
})
if err != nil { return err }
handler, err := binding.Query(callback)
if err != nil { return err }
```
