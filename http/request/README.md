# 请求参数 DTO

协议扩展：[表单与流式上传](FORM.md)、[XML 与原始报文](XML.md)。Form 复用 Prepared 默认值和存在性；XML 使用原生 xml 映射与独立值校验，不承诺 JSON 的缺失/null 语义。

`http/request` 负责输入来源、名称、存在性、默认值和类型转换，不导入路由器或第三方标签引擎。完整的强类型 HTTP 接口使用 [binding](../binding/README.md)；需要自行组合时使用 Prepare。

```go
type ListQuery struct {
    Current int64 `d:"1" v:"gte=1"`
    Size int64 `d:"20" v:"gte=1,lte=100"`
    Active *bool
}
plan, err := request.Prepare[ListQuery](request.Options{Source: request.Query})
if err != nil { return err }
// 在 handler 内：
input, states, err := plan.Decode(w, r, nil)
```

Prepare 检查声明并生成可并发复用的计划；每次 Decode 创建新 DTO 和状态。Decode 不执行 v 值规则或自定义 Validate，应把 states 交给 tags.Prepared.Validate，或直接使用 binding 完成合并和错误排序。Fields() 返回字段声明的独立快照，供检查名称、类型、默认值及嵌套关系使用。

## 名称、类型和边界

默认外部名称为 snake_case：Current → current、UserID → user_id、HTTPCode → http_code。json 指定名称，query/path 可覆盖对应来源的名称；只接受最终名称，不兼容多个别名。json:"-" 排除字段，排除字段不得声明 d/v 或来源标签。匿名 struct 可以展平，名称冲突、不可写字段、递归 DTO、旧 validate/default 标签在准备阶段拒绝。

Query/Path 支持平铺标量与指针，Query 还支持以重复键传入的标量切片。文本布尔仅 true/false，整数保持精度，浮点必须有限，不隐式 trim 或把空串改为零；时间点使用 Unix 毫秒。time.Duration 禁止隐式单位，使用带 _ms 的整数字段。Path 必须存在，不能声明 d，不重复解码路由器已解码的路径。

JSON 支持有界嵌套 struct、切片和 optional.Field，拒绝未知字段、重复对象键、多段 JSON 与错误类型。数值不默认接受字符串，确需字符串 ID 时显式使用 json:",string"。嵌套对象未出现时，不凭空创建对象以填入其子字段默认值。

默认 JSON 1 MiB、Query/Path 4096 字节，最大深度 16、集合元素 1000、声明字段 64；Options 的零值选择默认值，负值拒绝。解析/转换错误为 400，大小或结构资源超限为 413，JSON 媒体类型错误为 415。声明错误为 ErrSchema，运行时按 500 处理。取消/超时保留原有语义。

## 缺失、null 与 PATCH

| 输入 | 行为 |
|---|---|
| 缺失且有 d | 转换默认值；原始 Present 仍为 false |
| 显式 0、false、空字符串 | 保留输入，不套默认值；数值/布尔空串转换失败 |
| JSON null 标量 | 拒绝 |
| JSON null 指针 | 保留 nil |
| optional.Field 未传 | Present=false，业务不调用 setter |
| optional.Field null | 仅 nullable:"true" 允许；业务明确调用 Clear |
| optional.Field 具体值 | Present=true，Null=false，业务调用 Set |

d 只支持标量及其指针，不支持切片、复杂对象或 optional.Field；不能与 present 同用。PATCH 不从 Ent nullable 或 Immutable 推导接口可写性。示例见 [Ent 商品](../../examples/ent/README.md)。

## 低层显式解码

DecodeQuery、DecodePath、DecodeJSON 继续作为独立低层入口。DecodeQuery/Path 要求显式来源标签，仅处理平铺 string/bool/整数；DecodeQuery 可传默认 DTO。DecodeJSON 使用 DecodeOptions 限制请求体，至多一个，多余按 ErrSchema 拒绝。它们不会自动获得 Prepare 的名称推导、d/v 规则、嵌套状态和完整错误收集能力；使用这些能力请选 Prepared 或 binding。低层入口的超限语义与上文一致：原始输入超过大小上限映射 413，不再按字段校验错误返回 400。

DecodeJSON 与 Prepared 共享重复对象键和结构资源检查：重复键为 400，深度、对象字段或数组元素超限为 413。DecodeOptions 可配置 MaxDepth/MaxFields/MaxItems，零值分别使用 16/64/1000，负值按 ErrSchema 拒绝。低层 DecodeJSON 仍使用 encoding/json 完成目标类型转换，支持非 null 标量与自定义 UnmarshalJSON；Prepared 仍要求 struct DTO，两者的类型和存在性语义保持各自契约。

低层解码可能返回单个 FieldError，声明式绑定返回 Errors 集合。不要直接公开内部 error.Error()；由 respond 统一映射为安全消息和 null data。授权、排序白名单、数据库检查与事务留在业务层。

JSON 两种入口都拒绝非法 UTF-8（400），不让 encoding/json 静默替换输入字符。

## 可信代理客户端 IP

`NewClientIPResolver("10.0.0.0/24,2001:db8::/64")` 在启动时解析显式 IP/CIDR，
`resolver.Middleware` 将结果写入请求 context，使用 `request.ClientIP(r)` 获取；
也可直接调用 `resolver.Resolve(r)`。空配置只使用 RemoteAddr，不信任转发头。
只有直接对端可信时才解析 X-Forwarded-For，从右向左取第一个不可信跳点；没有 XFF 时
使用 X-Real-IP。非法地址、zone、超过 4096 字节或 64 跳的链回退到直接对端；
非法 RemoteAddr 返回 `unknown`。配置最多 4096 字节和 64 项，IPv4-mapped 地址归一化。
不做 DNS，不读取 Forwarded，不从任意头推导认证身份；中间件快照不受 handler 修改头影响。

## 规范 ID 绑定（v0.1.3）

ID 字段声明为 `id.ID`（导入 `id`），例如 `ID id.ID`，即可统一 JSON 字符串、query、params 与 form 的规范十进制解析。JSON 不需要 `,string`；JSON 数字拒绝，避免客户端先丢失精度。指针、切片、嵌套与 optional.Field 保留各自的存在性语义。零是否允许由接口 `v` 规则决定。普通 int64 的既有转换规则保持不变；自定义命名类型的 UnmarshalJSON 并非 Prepared 的通用扩展点。
