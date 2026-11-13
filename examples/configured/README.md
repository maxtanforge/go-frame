# 可扩展配置与标准启动

业务声明自己的 `settings`，组合 `config.Config`；字段解析、来源追踪和脱敏由框架处理。`command.Execute` 统一命令分发，消费方保留信号、文件路径、依赖和退出码的控制。本例只启动 HTTP，不打开数据库或 Redis；相应配置的真实连通性不属于 `config check`。

在仓库根目录执行：

```sh
go run ./examples/configured version
go run ./examples/configured config check --config examples/configured/config.yaml
go run ./examples/configured config explain --config examples/configured/config.yaml
go run ./examples/configured run --config examples/configured/config.yaml --env-file examples/configured/dev.env.example --set business.message=hello
```

访问 `http://127.0.0.1:3000/hello`、`/healthz`、`/readyz`。默认仅回环监听；容器部署需要显式设置 `LISTEN_HOST=0.0.0.0` 等地址。Ctrl+C 进入应用排空及清理流程。仅运行 `run` 才设置进程信号处理器和打开日志/端口。版本、检查与解释命令无需网络，不创建日志目录。

配置优先级：代码默认值 < 显式 YAML < 显式开发 `.env` < 进程环境变量 < 重复的 `--set path=value`。未提供的层不参与覆盖；每层提供的非法值都会报错，不能靠后续覆盖隐藏错误。同一个 `--set` 路径重复提供会报错。`--env-file` 仅允许最终环境为 `dev`；没有自动搜索配置文件或修改系统环境。

业务字段示例：

```go
type businessSettings struct {
    Message string   `config:"message" env:"MESSAGE"`
    Labels  []string `config:"labels" env:"LABELS"`
    APIKey  string   `config:"api_key" env:"API_KEY" secret:"true"`
}
// 父字段：Business businessSettings `config:"business" envPrefix:"BUSINESS_"`
```

环境变量对应 `BUSINESS_MESSAGE`、`BUSINESS_LABELS`、`BUSINESS_API_KEY`。列表采用 JSON 数组，如 `["a","b,c"]`；`[]` 表示显式清空。字符串空值会覆盖默认值；空数字、空布尔、空 duration 报错。框架配置的既有环境变量名称保留，例如 `HTTP_PORT`、`LOG_LEVEL`，文件/CLI 路径在本例有 `framework.` 前缀。详情见 [配置说明](../../config/README.md)。

`secret:"true"` 的值在 explain 中固定为 `[REDACTED]`。对自定义 TextUnmarshaler 不调用任意格式化器，显示 `[custom]`。校验回调错误对外仅显示通用验证失败，避免自定义错误泄漏秘密；调用方可另外实现只包含安全字段/规则信息的业务诊断。切勿直接打印 `Result.Value`。

YAML 限制为单份映射文档，最大 1 MiB；拒绝别名、锚点、自定义标签、null、重复键、带点键、空的嵌套映射和嵌套列表。根 `{}` 合法。标量/列表值最大 64 KiB；深度和节点数量受限。字符串形式日期需加引号。未知配置路径由消费结构校验。

`.env` 最大 1 MiB，最多 4096 项，只接受 `KEY=VALUE`、空行和整行注释。允许外层单/双引号；不做转义、变量插值、内联注释或 shell 执行，`export` 不被支持。仅已声明的环境键会被读取，其他键忽略。生产密钥从部署环境注入，不写入样例文件或命令行。

版本可通过构建变量注入，例如：

```sh
go build -ldflags "-X main.version=v0.2.0 -X main.commit=example -X main.buildTime=2026-09-28T00:00:00Z" -o configured ./examples/configured
```

本例是可复制的消费入口，不是自动生成工具。复制时需包含本目录全部生产 Go 文件；自动生成器、`frame init/doctor` 和 CLI 安装分发属于后续切片。独立消费验证使用临时 module 的显式本地 replace 验证工作区代码；正常项目应使用发布版本和正确的私有 module 认证。

配置检查的字段错误支持 `errors.As(err, &field)`（`field` 为 `*config.FieldError`），可获取字段路径、环境变量名、来源、期望类型和规则码。例如非法 `host` 会定位到 `host`/`LISTEN_HOST`，嵌套框架日志级别会定位到 `framework.log.level`。业务校验返回 `config.Invalid(path, rule)`，嵌套校验用 `config.PrefixValidation` 组合；不返回含配置原值的自由文本。规则码表示错误类别，具体业务上限仍由业务校验定义。

## 受控配置更新

```sh
go run ./examples/configured run --config examples/configured/config.yaml --watch-config
```

只监听明确指定的 YAML/.env 文件。默认每 500ms 检查内容并去抖，按白名单更新：

| 可动态修改 | 必须重启 |
|---|---|
| `framework.log.level`、`business.message`、`business.labels` | host/端口、服务标识、连接配置、密钥、TLS、日志文件目标及其余全部字段 |

动态字段与静态字段混合修改会整批拒绝，保留旧配置。非法 YAML、校验失败、文件暂时丢失也不会关闭服务；文件恢复后重试。删除文件中的字段意味着回到启动默认值/其他高优先级来源，并仍然接受静态字段检查。环境变量和 CLI 覆盖在启动时固定，不会因为修改父进程环境自动刷新。

`/hello` 响应中的 `config_version` 从 1 开始，成功变更递增；有效值未变化不递增。一次请求只获取一次独立快照，数组不会与内部快照共享。失败日志只给出 `invalid_configuration`、`restart_required` 等安全原因及当前版本，不打印配置、秘密或文件路径。日志级别的赋值和配置快照不承诺跨资源事务；提交仅用于已校验、不会失败的内存操作。

`config check/explain` 即使传入 `--watch-config` 也只检查一次，不启动 watcher、不打开端口。首次 watcher 回调会补查初始加载与监听启动之间的修改。多文件更新不提供文件系统事务，推荐一次写入完整配置并原子替换；Windows 编辑器可能需要在短暂读句柄冲突后重试。轮询无法检测两次观察之间发生又完全恢复的变化。

## 开发重启

从仓库根目录运行 `scripts/dev.ps1`（Windows）或 `sh scripts/dev.sh`（Unix）；两者都调用同一个 `go run ./cmd/frame dev`。Go 源码变化重新构建；YAML 变化走上面的动态更新流程。监督器、平台差异及验证限制见 [开发指南](../../docs/development.md)。

来源解析复用可选的 [`config/loader`](../../config/loader/README.md)。本例保留业务配置、
校验、开发 dotenv 政策及热更新管理器，显式启用 watch 参数并负责启动 watcher。
加载器构造时通过 `config.Prepare` 检查结构和捕获默认值，业务必填校验在每次 Load
合并全部来源后执行。配置中的 `framework.id.node` 属于静态字段，默认 0，用于雪花请求关联 ID。

basic、records 使用 `app/startup.Run` 共用日志装配；本例需要直接调用日志运行时的
SetLevel，因此保留显式的 logger.Open/Close 所有权。
