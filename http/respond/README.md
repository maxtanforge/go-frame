# HTTP 响应与错误

`respond.Adapter` 将返回 `(any, error)` 的 handler 转为 HTTP handler，统一响应包络、
错误映射与 panic 处理。请求解码位于 [request](../request/README.md)，公开错误类型位于
`http/httperror`；这些组件不要求业务用例依赖 HTTP。

```go
import (
    "github.com/maxtanforge/go-frame/http/httperror"
    "github.com/maxtanforge/go-frame/http/respond"
)

// 在 respond.Handler 中返回；cause 仅用于内部诊断。
return nil, &httperror.Error{Status: 409, Code: 1001, Message: "订单状态冲突", Err: cause}
```

示例是 handler 内的片段。`Message` 公开给客户端，`Err` 保留内部原因；
HTTP 边界统一使用 `httperror.Error`。

## 响应契约

```json
{"code":200,"data":null,"message":"成功","success":true,"timestamp":1790000000000,"traceId":"..."}
```

成功默认为 HTTP/code 200；nil 数据输出 null，`respond.WithMessage` 修改成功文案。
`timestamp` 是 Unix 毫秒；业务的 `time.Time` 仍遵循 encoding/json，需要毫秒整数时
显式使用 `respond.MillisTime`。分页直接返回 [pagination.Page 或 Slice](../../pagination/README.md)。

| 情况 | 行为 |
|---|---|
| `httperror.Error` | Status 限于 400–599，否则记录 ERROR 并返回安全的 500（忽略公开 Message/Code）；有效状态的 Code <= 0 时使用 Status；Err 不输出 |
| `validation.Errors` | 400，仅首条安全 Message，data 为 null；业务 `%w` 包装的集合经 errors.As 同样识别，完整集合仍可供内部读取 |
| 低层 `validation.FieldError` | 400，安全通用消息，data 为 null |
| `validation.ErrLimit` | 400，安全通用消息；与 ErrSchema 合并仍为 500，不公开部分校验结果 |
| `principal.ErrUnauthenticated` | 401，添加 WWW-Authenticate: Bearer |
| 未知错误、序列化失败、未提交前 panic | 500，公开通用文案并保留内部日志 |
| 请求 context 的 deadline 到期 | 响应未提交时 504 |
| 子操作返回 DeadlineExceeded，请求 context 仍有效 | 按显式 httperror.Error 映射，否则走正常错误分类；裸超时为 500 |
| 请求 context 已取消 | 不追加写出；按 DEBUG 记录一次，默认级别不输出；单独的业务操作取消不等于请求取消 |
| 响应已提交后错误／panic | 不追加包络；panic 通过 ErrAbortHandler 中止传输 |

错误匹配有顺序：请求取消、请求 deadline 优先于公开错误；显式 httperror.Error 的内部原因
不会再被展开为客户端字段诊断。直接写 ResponseWriter 会绕过成功包络；流式接口自行
管理输出。原生 handler 可用 `respond.Recoverer`，需要输出错误时用 `respond.WriteError`，
必须在提交响应前调用。超时是协作式取消，框架不强制终止业务 goroutine。
提交前同时检查取消状态和绝对截止时间；即使 deadline 定时器尚未发布取消状态，
已经过期的响应也返回 504。JSON 序列化耗尽预算时同样执行此检查。

字段错误集合没有安全消息时使用“请求参数不正确”。ErrSchema 为声明错误，返回 500；适配器不修改原始集合，不从任意内部 error 文本生成公开消息。

自动校验映射先检查整条错误链的 Schema 分类：声明错误与字段错误或集合通过
`errors.Join` 合并、再经 `%w` 包装，仍返回 500 并保留内部日志。`request.ErrSchema`
同时归入 `validation.ErrSchema`，诊断文字与原有 `errors.Is` 匹配保持有效。
请求取消、deadline 和显式公开错误的优先级仍按上面的契约执行。

完整字段、序列化和时间示例见[根文档](../../README.md#响应与错误)。
