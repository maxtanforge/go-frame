# 加密令牌到操作日志的接线示例

运行 `go test ./examples/authenticated -count=1`，无需监听端口或连接数据库。
测试生成临时内存密钥，完成 Issue → 公开/受保护接口 → Principal → audit.Actor →
操作日志的全过程。不会打印令牌、落盘密钥或提供接受任意 user_id 的签发接口。

`routes.go` 可复制到业务 bootstrap：传入 verifier 与日志 Sink，用
`router.NewAuthenticated` 注册接口，`Public: true` 表示免 access token 认证。
GET/HEAD 健康端点在示例中显式公开，其余新接口默认受保护。

生产业务通过已有 config/loader 加载自己的 AuthSettings，密钥字段使用 `secret:"true"`；
不要扩充所有项目共享的 config.Config。配置检查只校验结构，运行入口再将受管理的
公钥／32 字节加密密钥注入 token.NewVerifier。私钥只提供给签发端，不能提交到仓库。
多副本使用明确分发的 key ID/key set，轮换顺序见 token 文档。

完成业务账号凭据验证后调用 issuer.Issue；向客户端返回到期毫秒时用独立 DTO：

```go
type AccessResponse struct {
    AccessToken string `json:"access_token"`
    TokenType string `json:"token_type"`
    ExpiresAt int64 `json:"expires_at"`
}
response := AccessResponse{issued.AccessToken, issued.TokenType, issued.ExpiresAt.UnixMilli()}
```

token.Result 的 ExpiresAt 是 time.Time；不能直接把其默认 JSON 日期编码当成毫秒协议。
业务成功写入资源后可调用 operationhttp.SetResource；audit.Actor 传给事务内用例。
名称来自签发快照，账号禁用／会话检查可通过 AuthenticatedOptions.Check 显式接入。

框架提供认证和观测机制，不提供账号库、登录业务、刷新令牌或权限菜单。
示例通过命名 `Response` 阶段设置 Cache-Control；401/403 同样经过此阶段。
自定义权限使用 AuthenticatedPhase，处理已绑定 DTO/结果使用 binding.Intercept。
重定向直接返回 301/302 与 Location，不修改匹配请求再进入 next。
独立 service 模板固定 v0.2.2，包含当前导入路径和启动 API。
本地联调可显式 replace 到框架工作区。
