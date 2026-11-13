# 请求身份

`Principal` 是只包含 ID、Name、Issuer、TokenID 字符串的值快照；只依赖标准库。
ID 最多 128 字节，Issuer 最多 256 字节，两者必填；Name/TokenID 可空，最多 128 字节。
字段要求有效 UTF-8，不含首尾空白或控制字符。名称是签发时的快照。

- `IntoContext(ctx, value)` 供可信入口发布身份，不能直接采用客户端身份头。
- `FromContext(ctx)` 返回 `(Principal, bool)`，无身份或非法值返回 false。
- `Require(ctx)` 返回身份或 `ErrUnauthenticated`；respond 将该错误转为 401。

不包含原始凭证、任意 claims map、权限列表或全局 CurrentUser。IntoContext 只是
可信代码间传值，不进行密码学验证。后台任务应明确使用自己的系统审计人。
`database/audit/principal.FromContext` 是独立适配，audit 与 principal 核心不互相依赖。
