# 使用 AI 开发 Frame 业务服务

Frame 提供可组合的 Go 基础能力，业务代码由业务仓库维护。
AI 编写代码时，以所选框架版本的源码、能力文档和可执行示例为依据；历史规划不代表可调用 API。
没有工程或功能骨架生成器、通用接口扫描或内置 OpenAPI 导出；templates/service 仅作可编译的工程参考。

## 先确定上下文

先读取业务 go.mod，确认框架的实际版本及 replace；使用对应版本的文档，避免照着框架工作区的新接口修改旧版本业务。
没有版本标签也可以使用已推送提交，依赖解析后将伪版本与 go.sum 提交到业务仓库。
本地 replace 只证明本地消费，不证明远程版本具备同样能力。

按任务读取最小相关资料，不要求 AI 一次加载整个仓库：

| 任务 | 入口与可执行参考 |
|---|---|
| 初始化、配置、资源和启动 | [CLI](../cmd/frame/README.md)、[服务模板](../templates/service/README.md)、[开发指南](development.md) |
| HTTP 输入、默认值及校验 | [binding](../http/binding/README.md)、[request](../http/request/README.md)、[建模](modeling.md) |
| Ent CRUD、PATCH、事务与审计 | [Ent 综合示例](../examples/ent/README.md)；重点阅读 model.go、service.go、composition.go 与 service_test.go、composition_test.go |
| 默认认证、公开接口及操作日志 | [认证接入](../auth/http/README.md)、[authenticated 示例](../examples/authenticated/README.md) |
| XML 回调、表单与上传 | [protocols 示例](../examples/protocols/README.md) |
| 定时任务与业务 worker | [background 示例](../examples/background/README.md) |
| 目标用户授权、强类型下游及嵌套 PATCH | [profile 示例](../examples/business/internal/profile/README.md)；区分授权拒绝与依赖故障 |
| 设备回调去重、失败恢复及任务准入 | [devicesync 示例](../examples/business/internal/devicesync/README.md)；已接收不等于已完成，重放需要远端幂等契约 |
| 能力选择与分层 | [能力目录](packages.md)、[架构](architecture.md)、[业务装配顺序](business-integration.md) |

Ent 示例可验证认证→DTO→事务→身份审计→操作日志→指标的接线，不需要复制另一套全量示例。
模板提供轻量起点，默认没有认证；受保护业务必须显式装配认证，不能将模板默认设置误认为安全策略。

## 代码放在哪里

业务入口沿用 `cmd/api → internal/bootstrap → internal/<feature>`。
已有模板将协议代码放在 `internal/<feature>/http`；internal 表示 Go 导入范围，不是业务层级。优先遵循业务工程已有组织方式，
不用为很小的用例强制增加仅透传的 service/store/interface。

- bootstrap 拥有配置、依赖构造、路由和生命周期；SQL、Redis、日志由创建它们的所有者关闭。
- HTTP 层持有输入/输出 DTO，执行绑定并映射公开错误；业务用例接收 context 和明确的业务值。
- 持久化层使用所选 MySQL 或 PostgreSQL；采用 Ent 时由业务维护 Schema、生成代码与版本化迁移。
- 共享字段值规则放在业务的纯值包，供 DTO 和 Ent Schema 调用。请求实体、数据库实体和响应实体不强行合并。

不要增加全局 DI、BaseService/BaseRepository、运行时反射注册或全字段 DTO→Ent 复制。
跨实体写入由用例显式拥有事务；数据库连接、认证身份与资源所有权不藏在全局变量中。

## 接口必须写清的行为

AI 实现前，应明确方法/路径、输入来源、成功响应、公开错误和认证要求。
在业务测试中验证这些行为，不只检查代码能编译。

- 默认值只作用于缺失输入，显式 false、0、空字符串不能被默认值覆盖。
- PATCH 区分未传、null 和具体值；只写白名单业务字段，操作者与审计字段从可信上下文取得。
- 领域时间使用 time.Time，对外按约定输出毫秒；雪花 ID 对 JSON 客户端输出十进制字符串。
- Page 使用 current/size/total/pages/records；无总数查询使用 Slice，不伪造 total。
- 底层保留完整校验 Issues；HTTP 返回首条安全消息。声明或基础设施错误不能统一改为 400。
- 自定义组合条件由业务校验实现；依赖查询、权限与事务不写成通用标签规则。
- context 传到 SQL、Redis、外部 HTTP 和后台处理；独立业务错误不能被停机取消掩盖。

普通 HTTP 用强类型 binding 或 respond.Adapter；XML、文件和流式输出按各自协议处理。
公开路由明确登记，其余默认认证；平台公开回调还需要业务验签。

## 如何验证

在业务仓库执行其自身检查，至少包括：

```sh
go test ./... -count=1 -timeout=5m
go vet ./...
go build -buildvcs=false ./...
```

目标业务测试覆盖正常请求、认证/DTO 拒绝、默认值与零值、PATCH 三态，以及相关事务提交/回滚。
只对改动涉及的行为扩展测试；没有数据库的业务不需要虚构数据库验收。
框架根目录测试不会覆盖独立模板 module，业务测试通过也不会证明框架所有适配都可用。

修改框架、模板或公共接口时，在 Frame 仓库运行 `./scripts/verify.ps1` 或 `sh scripts/verify.sh`
（包含格式、测试、vet、固定版本 lint 和构建；首次获取 lint 工具需要网络），
并根据改动运行独立消费验收：

```sh
go run ./scripts/consumer --repo . --mode workspace
# 修改 HTTP 装配或生命周期时，显式开启回环进程验收
go run ./scripts/consumer --repo . --mode workspace --process --budget 10m
```

上述工具属于框架源码，不是业务项目需要安装的运行时依赖。
默认离线，需要已缓存依赖；首次缺失时显式加 --network 后再验证离线运行。
远程模式必须选定已推送提交，它不能证明尚未提交的工作区接口可从远程获取。
实际命令、实库选择及报告字段见[开发指南](development.md#独立消费工程验收)。

最终说明需区分单元/mock、实库、平台运行及跳过；race 需要支持 cgo 的工具链。
数据库/Redis 只使用明确提供的测试连接，不从其他工程自动寻找生产配置。
日志、测试快照和报告不记录凭证、DSN 或原始敏感报文。

## 可用于业务任务的提示

以下是任务约束示例，按业务现有结构裁剪：

> 先检查本工程 go.mod 中 Frame 的实际版本，再阅读对应能力文档和示例，确认 API 存在。
> 沿用现有 feature 分层和显式依赖装配，实现本次业务用例；不要添加通用生成器或全局容器。
> 明确路由的认证要求、请求默认值/缺失/null、写字段白名单和响应形状。
> 复用现有 binding、事务、审计和错误能力，避免重复封装基础代码。
> 对修改涉及的正常及失败路径运行测试，再运行本工程 vet/build。
> 报告实际修改、执行命令及未验证项，不把跳过的实库或平台检查说成通过。

## OpenAPI 的取舍

OpenAPI 由业务层定义和维护，不纳入 Frame 建设范围；框架不提供导出适配、接口 DSL 或文档托管。
当业务出现前后端分仓、多端客户端、对外 API 或 SDK 需求时，由业务选择契约工具并维护可验证的接口描述。
Frame 保持现有请求绑定、校验和响应能力，不为文档生成调整核心路由和 DTO。
AI 生成的契约仍需核对实际请求/响应、认证和错误行为，不能靠静态文档代替业务测试。
