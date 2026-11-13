# XML 接入

`ReadXML(w, r, XMLOptions{...})` 返回未经重写的完整报文字节，可用于业务验签。
`DecodeXMLBytes[T](ctx, raw, options...)` 在验签后解码；不需要验签时使用
`DecodeXML[T](w, r, options...)`。T 必须是结构体。解析使用原生 `encoding/xml`，
支持嵌套路径、属性、CDATA、命名空间和自定义 UnmarshalXML；不推断动态 map。
未知元素沿用标准库行为，默认忽略。需要严格字段白名单的协议应在业务层实现。

接受 application/xml、text/xml 和 application/*+xml。默认最多 1 MiB、
32 层、10000 个元素节点（含根节点）；零配置选默认值，负配置是声明错误。
仅允许一个根元素，拒绝 DTD/其他指令、根外非 XML 空白、畸形文档和超限输入。
大小/结构超限返回 413，格式错误 400，媒体类型错误 415。
上下文在读取和解析期间检查，但不能中断任意阻塞 Reader 或用户自定义解码函数；
必须设置 HTTP server 读取超时。请求 Body 仍由 HTTP server 关闭。

`binding.PrepareXML[T](binding.XMLOptions{...})` 创建可并发复用的解码与校验器；
注册时检查原生标签冲突、无效模式和不支持的字段类型，失败返回
`validation.ErrSchema`，低层入口对应 `request.ErrSchema`，均属于服务端声明问题。
`request.CheckXMLSchema(reflect.Type)` 可独立检查并按类型缓存结果。
检查先静态排除根解码接口，再提供无属性起始元素和立即失败的探针，由标准库解析标签；
不会调用 `UnmarshalXML`、`MarshalXML`、文本或属性编解码方法。
普通匿名嵌入保留原生标签优先级；递归匿名嵌入等不能安全解码的声明拒绝注册。
自定义 XML/文本解码类型的内部映射由业务拥有，作为 opaque 类型跳过检查；
其业务错误保持原样，不再统一变为客户端 400。原生元素名、格式和数值转换错误仍为 400。
原生映射不支持 map、interface、数组、函数或 channel；字符数据只接受原生标量、
字符串、字节切片或文本解码类型。原生指针最多一层（递归命名指针也拒绝）；
不经过结构体的递归切片链会拒绝注册，结构体的递归子节点集合仍支持。
自定义解码接口仍按 opaque 类型处理。扫描所有可见原生字段，包含被嵌入优先级遮盖的字段，
这些字段也应使用受支持类型或显式 `xml:"-"`。
每个公开读取/解码入口独立检查输入边界；普通 `DecodeXML` 复用 `ReadXML`
已检查的字节，只执行一次完整结构扫描和一次业务解码。
其 `Decode` 返回完整 `validation.Errors`。`binding.XML[Req, Res]` 组合
标准 `func(context.Context, *Req) (*Res, error)` 回调，成功直接输出 XML，
失败沿用安全 JSON 错误包络，只公开首条安全消息。

XML 校验复用 `v`、`label`、自定义 `Validate(*validation.Context)` 和消息配置。
诊断路径使用 `json` 标签或校验器默认名称，而 XML 映射只使用 `xml` 标签。
`XMLName xml.Name` 建议同时标记 `xml:"message" json:"-"`，避免校验框架
将命名空间元数据当作业务字段。`json:"-"` 仅排除校验，不改变 XML 解码。
不支持 JSON 的默认值/存在性绑定：注册时拒绝 `d`、`default`、`nullable`、
`present` 规则及 optional.Field（校验排除字段不受检查）。普通字段按解码后值
校验，空元素和缺失标量都可能是零值；业务需要区分时可使用指针和自定义校验。
`required` 表示非零值，不表示 XML 元素存在。原生解码/类型转换失败立即返回错误，
只有成功解码后的值规则收集完整诊断。

`respond.XML(w, r, status, value)` 与 `respond.Text(w, r, status, text)`
直接响应，不加成功包络。XML 完整序列化成功后才设置响应头；返回的错误可交给
既有 `respond.Adapter` / `respond.WriteError`。写出失败可能已提交响应，
不可再次手动写入错误包络。微信的验签、重放保护、解密和消息类型分发由业务负责。
