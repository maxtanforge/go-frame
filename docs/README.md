# 文档导航

当前实现以仓库代码、可执行示例和以下能力文档为准。公共能力采用
职责明确的能力包；发布和远程同步状态以 Git 为准。

## 先选入口

| 当前目标 | 阅读路径 |
|---|---|
| 理解服务如何启动 | [basic](../examples/basic/main.go) → [生命周期](../app/README.md) |
| 新建业务仓库 | [服务模板](../templates/service/README.md) → [业务装配](business-integration.md) |
| 实现完整业务功能 | [分类 CRUD](../examples/business/README.md) → [运行步骤](../examples/business/RUNNING.md) → [建模与分层](modeling.md) |
| 修改框架 | [仓库工作流](../AGENTS.md) → [验证指南](verification.md) |
| 发布框架版本 | [版本发布](releases.md) |
| 理解设计取舍和包边界 | [设计说明](design.md) → [整体架构](architecture.md) |

库组件按能力归类，业务代码按功能聚合；`internal` 表示 Go 导入可见性。
目录边界与依赖方向统一见[整体架构](architecture.md)，其他专项示例见[示例导航](../examples/README.md)。

## 按主题查阅

| 主题 | 使用说明 |
|---|---|
| 能力与接入 | [能力与使用参考](reference.md)、[能力目录](packages.md)、[架构分层](architecture.md)、[命名规范](naming.md) |
| 示例选择 | [示例导航](../examples/README.md)，三个起点、专项装配与入口实验 |
| 资源与事务接线 | [资源生命周期](../app/resource/README.md)、[显式事务示例](../examples/transactions/README.md) |
| 独立业务工程 | [服务模板](../templates/service/README.md) 与 [开发 CLI](../cmd/frame/README.md)，版本选择、启动与配置命令、可选数据库及 Redis |
| 综合接入顺序 | [业务装配指南](business-integration.md) 与 [Ent 综合示例](../examples/ent/README.md)，配置→认证→DTO→Ent→审计/事务→协议回调→后台执行 |
| 业务入口与类型组织 | [有类型入口示例](business-integration.md)、[Entity / VO / DTO](modeling.md)、[分类业务示例](../examples/business/README.md)、[完整 CRUD 运行步骤](../examples/business/RUNNING.md) |
| 配置与启动 | [配置模型和解码](../config/README.md)、[来源加载器](../config/loader/README.md)、[日志装配](../app/startup/README.md)、[应用命令](../app/command/README.md) |
| 开发体验 | [开发指南](development.md)、[配置热更新](../config/reload/README.md)、[configured 示例](../examples/configured/README.md) |
| AI coding | [业务开发与验证指南](ai-coding.md)，框架版本、示例选择、分层、接口语义及实际验收 |
| 入口声明对照 | [类型化入口示例](../examples/contracts/README.md) |
| 认证与操作日志 | [认证/公开名单](../auth/http/README.md)、[加密令牌](../auth/token/README.md)、[请求身份](../auth/principal/README.md)、[操作日志](../telemetry/operation/README.md)、[整链示例](../examples/authenticated/README.md) |
| HTTP 输入 | [query/path DTO](../http/request/README.md)、[安全字段校验](../validation/README.md)、[JSON 请求体](../http/request/README.md)、[响应包络与错误](../http/respond/README.md) |
| XML 与上传 | [XML 原始报文、值校验与响应](../http/request/XML.md)、[表单与流式上传](../http/request/FORM.md)、[业务协议示例](../examples/protocols/README.md) |
| 调度与互斥 | [本地定时任务](../schedule/README.md)、[Redis 租约](../lock/redis/README.md)、[业务 worker 注册与确认](../examples/background/README.md) |
| 健康探测 | [探测核心](../health/README.md)、[HTTP 端点](../http/health/README.md) |
| 实体、校验与 ORM | [建模指南](modeling.md)、[标签校验](../validation/tags/README.md)、[Ent 样例](../examples/ent/README.md) |
| 缓存与计数 | [本地 LRU、TTL、Redis 与 JSON 契约](../cache/README.md)、[Redis 固定窗口原子计数](../redis/README.md) |
| 分页与 SQL | [Page/Slice](../pagination/README.md)、[SQL 片段](../database/query/README.md)、[数据库](../database/README.md) |
| ID 与审计 | [雪花 ID](../id/README.md)、[审计元数据](../database/audit/README.md)、[records 示例](../examples/records/README.md) |
| 运行约束 | [生命周期](../app/README.md)、[日志](../telemetry/logger/README.md)、[业务服务运行约束](operations.md)；镜像与部署清单由业务仓库维护 |

## 当前契约

- 普通分页 `Page` 固定五个字段：current、size、total、pages、records。
  total/pages 为数字；无总数查询返回独立 Slice（current、size、has_more、records）。
  构造方法为 pagination.NewWindow/NewPage/NewSlice。
- Prepare 检查配置结构并冻结默认值，不执行业务校验；Decode/Load 合并来源后校验。
  basic、records 使用 app/startup.Run 管理日志；configured 自行持有日志运行时以更新级别。
- records 使用雪花 int64 主键和 SQL BIGINT，对外 ID 为十进制字符串。ID_NODE 默认 0，
  多实例必须显式分配不同节点，不能由 INSTANCE_ID 推断；领域/数据库时间采用 UTC 毫秒精度，records HTTP
  创建、详情及列表通过 DTO 输出 Unix 毫秒整数。
- 声明式 DTO 使用 binding.Query/JSON/Path/JSONWithPath；request.Prepare 提供独立输入计划，DecodeJSON/DecodeQuery/DecodePath 保留低层显式入口。
  声明式校验使用 validation.Errors 保留全部 Issues，由 respond 输出 400 的首条安全消息和 null data；
  普通错误及内部原因不自动公开。

## 交付与验证边界

验证入口见 [统一验证说明](verification.md)、[独立消费验收](development.md#独立消费工程验收)与 [集成测试](integration-testing.md)。按目标环境执行验证，未选择的测试不能视为通过。

Redis 租约、本地定时任务、XML/表单上传、Ent CRUD 辅助与错误分类已实现。
框架不提供游标批处理、通用乐观锁或跨副本任务周期去重；服务模板作为工程参考。
框架不提供工程或功能骨架生成；OpenAPI 由业务层定义和维护。
注册中心、权限框架、自动租户隔离不纳入当前建设范围。
