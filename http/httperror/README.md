# HTTP 错误契约

`httperror.Error{Status, Code, Message, Err}` 在请求解析和响应输出之间共享。
Message 是明确允许公开的文案，Err 保留内部原因。respond 优先处理请求取消和超时，
再映射 HTTP 错误；不会因为内部原因包含 validation.FieldError 而公开它。

业务领域可以返回普通 Go error，由 HTTP 入口选择状态码和公开文案，领域层不必依赖本包。
