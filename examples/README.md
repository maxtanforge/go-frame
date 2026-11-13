# 示例导航

库按公共能力组织；业务工程按功能聚合。先按目标选择下面三个入口，再按需阅读专项示例。

## 三个起点

| 目标 | 从哪里开始 | 读完或运行后应掌握什么 |
| --- | --- | --- |
| 理解并运行一个 HTTP 服务 | [basic/main.go](basic/main.go) | 配置、路由、健康检查与生命周期如何显式装配；不配置 SQL/Redis 也能启动 |
| 创建自己的业务工程 | [templates/service](../templates/service/README.md) | 复制独立 module，调整模块路径和依赖版本，在 bootstrap 装配按功能组织的业务 |
| 参考完整业务实现 | [business](business/README.md) → [运行步骤](business/RUNNING.md) | 五个分类 CRUD 入口，以及 DTO、VO、Handler、Service、Store、权限与事务的职责 |

basic 用于理解启动机制，模板用于建立工程，business 用于参考业务实现。完整分类服务需要数据库；
PostgreSQL 为默认推荐，驱动和连接地址由业务选择。业务只复制需要的工程或功能，不复制整个 examples 目录。

## 按需选择专项示例

| 目标 | 入口 | 组织方式 |
| --- | --- | --- |
| 配置与生命周期 | [configured](configured/main.go)、[transactions](transactions/README.md) | 配置组合与显式事务注入示例 |
| SQL 仓储、分页与事务 | [records](records/README.md) | 业务服务与 SQL 适配；运行前按说明配置存储 |
| Ent Schema 与生成客户端 | [ent](ent/README.md) | 保持 Ent 生成目录，业务工程可按功能抽出适配 |
| 认证与观测 | [authenticated](authenticated/README.md)、[observability](observability/README.md) | 身份、路由政策与遥测装配 |
| 档案授权与设备同步 | [profile](business/internal/profile/README.md)、[devicesync](business/internal/devicesync/README.md) | 强类型下游、嵌套更新、可靠接收、失败重放与有界调度；不默认挂载到分类服务 |
| 协议、后台与工具组合 | [protocols](protocols/README.md)、[background](background/README.md)、[toolkit](toolkit/README.md) | 各自能力边界的专项示例 |

## 入口实验

[contracts](contracts/README.md) 比较类型化组合助手与现有 binding 的声明方式；
[transactions](transactions/README.md) 中的 Meta 扫描也是比较原型。实验入口仅存在于示例内，未发布为公共 API。
业务常规入口使用公开的 binding 和 router.Route.Bind/Collect，签名以所选版本源码为准。

## 验证范围

根目录 `go test ./... -count=1` 覆盖 examples 的测试与编译；templates/service 是嵌套独立 module，需通过 `scripts/verify-service.ps1` 或 `scripts/consumer` 单独验收。测试通过不表示真实数据库、Redis 或遥测服务已经连通。
