# 操作日志

`telemetry/operation` 定义标准库事件和 `Sink.Record(ctx, Event) error`；`telemetry/operation/http`
负责请求观测。`NewSlogSink(lg)` 复用现有日志运行时、轮转和关闭流程。
v0.1.3的事件自动携带已验证身份的 `actor_id`、`actor_name`、`actor_issuer`。
账号／权限检查拒绝时仍保留身份；凭据验证失败和匿名请求不伪造操作人。
名称来自已验证 Principal 快照；持久化审计的事务范围和 Sink 仍由业务明确装配。

```go
recorder, err := operationhttp.New(operation.NewSlogSink(lg), operationhttp.Options{})
// 检查 err；挂载 recorder.Middleware 后，默认记录所有经过它的请求。
```

没有声明动作时，Action 使用方法与路由模板；匹配不到使用固定占位符。
实际请求路径记录在 `path`，保留 URL 编码，不含 query；超过 2048 字节时为 `<path>`。
认证拒绝和未匹配请求也记录 path，route 保留路由模板。
`operationhttp.With(Metadata{Action, Description, ResourceType, Mode})` 可覆盖路由信息。
Mode 为 Inherit/Enabled/Disabled；关闭 operation 不关闭 access 日志。同一请求嵌套
多个 recorder 只由最外层输出一条 operation，内层元数据仍可补充。

认证拒绝也要有动作时，使用 `router.NewAuthenticated`：它在认证前匹配路由模板和
声明元数据；单独把 With 放在 Require 后面，认证拒绝自然无法执行该路由中间件。

handler 可使用 `SetResource(ctx, kind, id)` 和 `SetFields(ctx, fields)` 显式补充数据。
两者要求请求经过 recorder.Middleware；只有 With 路由元数据仍返回 recorder missing。
该检查确认 recorder 已接入，是否输出仍遵循 Mode，持久化结果仍由 Sink 决定。
Fields 最多 16 个标量，键最多 64 字节、字符串值最多 512 字节；禁止嵌套对象、保留字段
和常见凭证键。输入 map 被复制。校验不能识别任意秘密内容，调用者仍需选择安全字段。
Recorder 默认采集 query / params / JSON body 的全部字段；接口元数据可选择字段或分别关闭采集，
`Options.Request` 可覆盖 recorder 默认策略，
日志格式配置与参数采集策略分别设置。

```go
router.Route{
    Method: "POST", Path: "/orders", Handler: handler,
    Operation: operationhttp.Metadata{
        Action: "order.create",
        Request: operationhttp.RequestOptions{
            Query: operationhttp.Enabled,
            Body:  operationhttp.Enabled,
            QueryFields: []string{"page", "limit"},
            BodyFields: []string{"product_id", "quantity", "contact"},
            SensitiveFields: []string{"phone_number", "email"},
        },
    },
}
```

挂载 `Recorder.Middleware` 后，也可使用 `operationhttp.With(metadata)` 声明路由组或
单个接口。Query / Params / Body 的 Inherit 保留外层声明，Enabled 开启，Disabled 显式关闭；
没有外层声明时继承 recorder 策略，默认开启。参数输出在 `event.request.query` / `event.request.params` / `event.request.body`。
QueryFields / ParamsFields / BodyFields 按原始键名精确选择顶层字段；nil 继承外层配置，未声明时保留全部
字段，显式空切片只输出空对象。BodyFields 要求 JSON 顶层为对象，否则记录 `not_object`。
SensitiveFields 添加按键名匹配的自定义脱敏字段，递归作用于对象和数组；同样忽略大小写、
下划线和连字符，内置凭证脱敏始终生效。Recorder、外层和接口的自定义名单累加，
按规范化键名去重；nil 和空切片均不移除已有保护。字段选择列表仍使用上述覆盖语义。
每份声明中的列表最多 32 项，每个键最多 128 字节；自定义敏感字段的合并集合可超过 32 项。
启动时校验并复制声明，导出的路由策略也独立复制。
params 使用路由匹配到的命名参数，不包含内部 `*` 挂载参数；没有命名参数时省略，
认证拒绝仍记录预匹配参数，保留路由器参数的编码语义。参数复制最多 16 KiB，超限只输出 `params_omitted: "too_large"`。
query 保留同名参数的多个值；开启 body 时只采集业务实际读取的字节，原请求体和 Close
行为不变，不预读或补读。业务没有读取 body 时记录 `body_omitted: "not_read"`，例如认证拒绝。
没有请求体时不输出 body 或省略原因。可用 `RequestOptions{Query: Disabled, Params: Disabled, Body: Disabled}`
在接口或 recorder 上同时关闭三种来源；接口声明优先于 recorder 默认配置。

采集支持 application/json 和 application/*+json。参数总输出最多 16 KiB，body 缓冲最多
16 KiB，嵌套深度最多 10。过大、不完整、非法 JSON 或不支持的类型只记录省略原因，
不影响接口响应。脱敏按键名（不区分大小写，忽略下划线和连字符）匹配 password、token、
secret、apiKey、authorization、cookie，值替换为 `***`；嵌套对象和数组也适用。
脱敏不能识别任意秘密内容，接口仍应评估其字段是否适合记录。认证头和 Cookie 头不采集。

日志 `kind=operation`，事件在 `event` 字段中；访问日志使用 `kind=access`。
Event 包括 action、description、资源与操作人、trace_id、timestamp（Unix 毫秒）、
duration_ms、method、path、route、status、outcome，以及按最终策略采集的 request。
认证仅自动记录 ID/Issuer，不记录 TokenID。
duration_ms 在 handler 返回或 panic 时立即确定，不包含参数整理和 Sink 输出耗时；
访问日志与请求完成指标复用这个结束时间。
同步 Sink 占用请求 goroutine，实际请求收尾包含这段时间；当前 duration_ms 与请求指标
不包含它。业务接入远程 Sink 时应单独观测输出耗时、超时和失败，避免仅凭请求指标判断负载。
Outcome 区分 completed/denied/failed/canceled/aborted/hijacked，不代表事务提交证明。

Sink 同步执行，默认使用独立 100ms 协作式超时，脱离请求取消但保留 context 值。
Sink 必须并发安全、遵守超时；框架不会用逃逸 goroutine 强制中止不配合的 Sink。
失败／panic 产生独立诊断，不改写业务响应；可用 OnError 观察失败，回调需快速返回。
slog 本身的输出错误报告遵循所用 handler 的能力，不承诺持久化成功。

响应观察不缓冲 body，保留 ResponseController、Flush、Hijack 和 ReadFrom 行为。
`status=0` 表示观察器未见最终响应提交，例如取消或恢复前的 panic。
需要与数据同事务可靠落库的审计事件，仍在业务用例内写入，不依赖请求收尾日志。

默认日志级别：Completed / Denied / Canceled / Hijacked 为 INFO，Failed 且状态为 4xx 时为 WARN，其余 Failed 与 Aborted 为 ERROR。结果分类不随级别改变。自定义 Fields 与请求脱敏共用 SensitiveKey 规则，忽略大小写、下划线和连字符，拒绝 password/token/secret/apikey/authorization/cookie 片段；业务仍须控制其他名称下的敏感内容。
