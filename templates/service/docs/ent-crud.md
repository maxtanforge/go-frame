# 在业务模板中接入 Ent CRUD

默认模板继续提供无数据库业务。需要 CRUD 时，在复制后的业务仓库执行下面的步骤，实体和业务代码归业务项目维护。先锁定包含 QueryPage、QuerySlice、NewSorter、WithTx、ErrorClassifier 的 Frame 提交；版本选择见默认模板 README。本地验证可使用临时 replace，部署前移除并锁定已推送提交。

## 接入步骤

1. 保留 bootstrap 的 database resource 生命周期，推荐 PostgreSQL（DATABASE_DRIVER 默认 postgres），数据库由业务按 DATABASE_DRIVER / DATABASE_URL 选择，生产只导入所选方言的连接适配器。
2. 执行 `go get -tool entgo.io/ent/cmd/ent@v0.14.6`，工具和运行时选择同一版本。执行 `go tool ent new Product` 创建业务 Schema；需要行锁时生成命令启用 `--feature sql/lock`。
3. 在 `ent/schema/product.go` 定义业务字段；组合 `entdb.IDMixin{}` 与 `entdb.AuditMixin{}`。事件实体可选择 CreatedMixin。字段规则由 `internal/productvalue` 纯函数共享，创建/PATCH 请求分别维护字段可写范围。
4. 在 `ent/generate.go` 写入下面的生成入口，运行 `go generate ./ent` 并提交生成代码。版本化迁移单独审查，在部署时执行；服务启动不自动建表。
5. database resource 就绪后，使用 `entdb.New(pool.SQLDB(), ...)` 构造借用连接池的 driver，创建生成客户端，并一次注册 NewAuditHook，通过标准 Mixin 自动识别实体；ID hook 按业务 ID 策略装配。Actor 默认使用可信请求身份；无用户的任务显式提供 system actor。
6. 构造业务 Service，启动期创建 NewSorter 白名单；注册 binding.Query/JSON/Path/JSONWithPath 处理器。数据库对象不直接作为 HTTP 响应。

`ent/generate.go`：

```go
package ent

//go:generate go tool ent generate --feature sql/lock ./schema
```

## 代码组织

```text
ent/schema/             业务持久化声明
ent/                    Ent 生成代码
internal/productvalue/  DTO 与 Ent 共用的纯值规则
internal/product/       业务模型、Service、仓储契约与事务边界
internal/product/http/  DTO、HTTP 注册、输出映射、公开错误消息
internal/product/store/ Ent 适配、显式 Set/Clear 与事务操作
internal/bootstrap/     连接池、hook、Service 与路由组装
migrations/             按方言审查的迁移文件
```

Service 中使用 `entdb.WithTx(ctx, client.Tx, callback)`；回调只使用 tx 客户端。列表从同一已过滤、尚未分页的 query 调用 QueryPage，排序使用 NewSorter 解析后的强类型 option。需要无总数结果时改用 QuerySlice；不要把未知总数伪装成零。

锁查询或自定义 SQL modifier 查询直接在事务内执行原生 builder，不传给 QueryPage/QuerySlice：当前 Ent Clone 不复制 ForUpdate/ForShare 所在的 modifiers。此时手动配置 Offset/Limit 并使用 pagination.NewPage/NewSlice 组装结果。

创建、详情、更新和删除继续使用 Ent 生成方法。PATCH 显式区分未传、零值和 null，通过 Set/Clear 操作，不用反射复制输入对象。ErrorClassifier 只返回类别，业务明确决定哪些错误可以公开。Constraint 不能直接当成“重复名称”，唯一性由数据库约束兜底。

## 完整业务与能力参考

完整可运行 CRUD 以框架仓库的 [examples/business](../../../examples/business/README.md) 和 [RUNNING](../../../examples/business/RUNNING.md) 为准：`internal/category` 管理规则和用例，`http` 中分开请求 DTO、响应 VO、命名 Handler、路由和错误映射，`store` 显式适配 Ent，`bootstrap` 组装资源、认证和审计。复制到业务项目时调整模块导入路径，并按业务 Schema 重新生成 Ent；不导入框架 examples 下的生成实体。五个 CRUD 端点默认保护，创建时先保留 parent 审计身份，成功后替换为新资源；身份审计不能替代授权。

`examples/ent` 仅作为 Ent 事务、分页、排序、审计及错误分类的能力参考，不作为完整业务项目组织模板。实体、事件 Schema、迁移、权限和事务范围均由业务项目维护。

验证业务项目的单元测试、编译、事务回滚及所选方言迁移。框架 SQL mock 测试不是业务数据库实连验证；服务模板验证的本地 replace 也不证明私有模块远程下载成功。
