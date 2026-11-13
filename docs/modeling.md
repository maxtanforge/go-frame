# Schema、入参校验与数据库映射

Frame 推荐 Ent 的 Schema 与生成客户端工作流。Ent 为可选数据访问扩展，SQL 连接层保持独立。CRUD 由业务使用生成的 Ent 客户端实现。

## 职责划分

| 结构 | 维护内容 |
|---|---|
| 业务 ent/schema | 字段、列类型、索引、关系、字段值校验和公共 Mixin |
| Ent 生成代码 | 实体、类型安全谓词、Create/Update/Query、事务客户端 |
| 创建/PATCH DTO | 客户端可写字段、必填与缺省/null 语义 |
| 响应 DTO | 公开字段白名单、字符串 ID、毫秒时间 |
| 业务 migrations | 经审查的版本化 SQL，部署时显式执行 |

集中维护存储模型，不意味着所有接口共用一个实体。数据库 NOT NULL 不等于 PATCH 必填；JSON 名称也不决定数据库写权限。字段值规则可抽成业务小包，由 Schema Validate 和 DTO Validate 共用，完整例子见 [productvalue](../examples/ent/internal/productvalue/rules.go)。请求由 binding 的 d/v/label 声明和 Validate(*validation.Context) 校验；DTO 用 validation.Check 绑定纯值规则的相对 Issue，Ent 直接 .Validate 同一函数。框架不会从 Ent 自动派生 DTO、Set/Clear mapper 或 OpenAPI。

## 业务 Entity、VO 与 DTO

按业务语义选择类型，按业务模块组织文件：

| 类型 | 用途 | 建议位置 |
|---|---|---|
| Ent 实体 | 数据库字段、关联和查询对象 | 业务工程的 `internal/ent`，保持生成器布局 |
| 业务实体 | 有身份和生命周期的 Category、Book、Transaction 等模型 | 所属 feature 的 `model.go` |
| VO（Value Object） | 金额、记账日等无独立身份的业务值及规则 | 单域的 `value.go`；跨域确实复用时建立业务值包 |
| 命令、查询条件 | Service 输入，使用业务类型 | `command.go` / `query.go`，简单模块可留在 service.go |
| 查询投影 | 详情、统计等用例需要的字段组合 | `query_result.go`，简单模块可留在 model.go |
| 请求、响应 DTO | HTTP 字段、输入状态和公开输出表示 | 所属功能的 `http/dto_request.go` / `http/vo_response.go` |

本表 VO 指值对象；仓库示例与模板以 `http/vo_response.go` 和 `XxxVO` 命名响应视图对象，是另一种团队用法——同一工程内固定一种语义：值对象放 `value.go`，视图对象放 `http/vo_response.go`。不要建立全局 vo/entity/dto 三套目录，也不要求每个模块创建表格里的所有文件。

例如用户提供的 sino-boot 规范将 API 的 Req / Res 称为 VO，将 Logic 的 Inp / Out 称为 DTO。它与本文的术语划分不同，但同样可以表达传输边界与业务边界。消费项目可以沿用 Req / Res、Input / Output 的命名；frame 不强制包名。团队需明确 VO 指视图对象还是值对象，业务服务也不必因目录规范直接依赖 API 响应。

```text
internal/category/
  model.go          # 业务实体与小型查询投影
  service.go        # 业务规则、权限和事务编排
  store/            # 有实际持久化需求时添加，显式映射 Ent 与业务模型
  http/
    routes.go       # Method / Path / Public / Operation
    handler.go      # 传输适配、主体转换、业务错误映射
    dto_request.go  # 请求类型；转换为业务命令
    vo_response.go # 响应视图类型；从业务结果转换
  *_test.go         # 主要验证业务规则和数据一致性
```

模板与业务契约示例按功能聚合，用 http 子包隔离协议依赖；很小的业务也可在同 package 内按文件分工，见[架构指南](architecture.md#业务侧如何分层)。业务实体通常使用 time.Time、数值 ID 和领域值，HTTP DTO 才选择字符串 ID、respond.MillisTime 和 JSON 标签。简单只读结果仅用于输出时，可直接定义响应 DTO；不强制每张表经过 Entity → VO → DTO 的复制链。

业务不变量应保护非 HTTP 调用。比如交易金额的精度和范围，除了请求格式检查，Service 从同步、AI 或后台任务接收值时也必须得到相同保护。可复用纯值规则或值对象，但不能只依赖 HTTP 的 v 标签；构造函数也需要明确 Go 零值的有效性。交易正金额、余额、统计汇总和预算阈值未必共享同一个范围，不宜合并为万能 Money。

PATCH 从 DTO 转成业务命令时必须保留语义。普通 *T 只有两种状态，不能同时表示省略、null 和具体值；只有业务明确将 null 与某个零值视为相同操作时才能合并。否则保留 optional.Field 或明确 Set/Clear。响应白名单采用显式转换，避免把 PasswordHash、内部凭据或未来新增的 Ent 字段暴露出去。

[分类业务契约示例](../examples/business/README.md) 在 `internal/category` 内聚合业务包与 `http` 适配子包（包名 categoryhttp）：规范 ID 规则留在业务包，HTTP 通过自定义字段校验复用；PATCH 的 `optional.Field` 在边界转换为显式业务更新命令，业务不依赖 JSON 三态类型。名字、状态和所有权规则同时保护 HTTP 与非 HTTP 调用；存储更新契约要求载入、权限检查和写入处于同一原子操作中。

这个示例展示依赖隔离，不意味着小模块都要拆子包。若将 HTTP 与业务放在同一 feature package，仍可按 model/service/store/dto/http 文件分工；当需要限制导入方向时，再拆适配子包。值对象的构造函数不保证其 Go 零值有效，服务入口仍检查业务值，不能仅靠类型名视为通过校验。

## Entity、模型与 DTO 的转换配方

各跳点转换由所属 feature 手写显式函数，框架不提供整结构自动映射：反射推导违反显式声明约定，也会绕过响应白名单审查。不建全局 converter/mapper 包，函数就近放在使用它的边界文件里，每个通常 3–15 行。三类跳点的标准写法（完整参考[分类业务契约示例](../examples/business/README.md)）：

| 跳点 | 所在文件 | 命名 | 固定规则 |
|---|---|---|---|
| Ent 实体 → 业务模型 | `store` 私有函数 | `snapshot` | 返回独立值快照，不共享实体指针；nullable 列转值 + 布尔（`*string` → `Note` + `HasNote`）；数字 ID 经值对象构造函数 |
| 请求 DTO → 业务命令 | DTO 的方法 | `command` | `optional.Field` 三态一次性解码为 `Set`/`Clear`；函数只映射字段，不重复业务校验 |
| 业务模型 → 响应 DTO | `http` 私有函数 | `public` | 字段白名单、字符串 ID、毫秒时间；只输出公开字段 |

两个易错点在示例中都有对应写法：nullable 指针输出先复制局部变量再取址（`note := m.Note; out.Note = &note`），直接 `&m.Note` 会暴露可从外部修改的模型字段别名；三态组合逻辑（`Set: f.Present && !f.Null`，`Clear: f.Present && f.Null`）集中在一个 `command` 方法里，不在多个调用点重复展开。

## 开发流程

业务项目中安装锁定版本的官方生成工具：

```sh
go get -tool entgo.io/ent/cmd/ent@v0.14.6
go tool ent new Product
```

ent/generate.go：

```go
package ent

//go:generate go tool ent generate --feature sql/lock ./schema
```

修改 Schema 后运行 `go generate ./ent`，提交生成代码。工具与运行时共享 go.mod 的同一 Ent 版本，不需要全局安装。Frame 不把业务的 Schema 或生成客户端放进框架包。

```go
func (Product) Mixin() []ent.Mixin {
    return []ent.Mixin{entdb.IDMixin{}, entdb.AuditMixin{}}
}
```

推荐 PostgreSQL。在入口创建连接池，使用 `entdb.New(pool.SQLDB(), entdb.Options{Dialect: "postgres"})` 构造 driver，交给业务生成包的 `ent.NewClient(ent.Driver(driver))`。随后为使用完整 Mixin 的实体安装 `NewMutationHook`，注入雪花 ID、Actor resolver 和可选时钟。完整代码见 [main.go](../examples/ent/main.go)；无反射 DI 或全局单例。

## PATCH、事务与分页

`optional.Field[T]` 保留缺省、null 和值三态。在服务中使用 Ent 的 `SetPrice(0)`、`SetActive(false)`、`ClearNote()`，不会遗漏零值。未知字段在 JSON 解码阶段拒绝，系统字段从请求 DTO 中排除。非 HTTP 调用通过 tags.Prepared.Validate 校验显式构造的 DTO（无法恢复请求缺失信息），数据库唯一约束和业务状态仍需单独处理。

业务使用 `entdb.WithTx(ctx, client.Tx, func(tx *ent.Tx) error { ... })` 收敛事务收尾逻辑，仍然使用原生 Ent Tx。事务中只使用 tx 客户端；不能假定根客户端或 Frame SQL WithTx 会自动加入。回调/取消错误与回滚错误合并，提交失败不自动重试。示例将商品写入和事件写入放在同一事务，证明事件失败时不会残留商品变更。

分页使用 `pagination.NewWindow` 和 `entdb.QueryPage`，从同一尚未分页的过滤查询克隆 Count 和列表，返回强类型 Page，无记录输出 []，五字段契约保持不变。不计总数使用 QuerySlice。通过 `entdb.NewSorter` 将公开别名映射到具体 Ent 排序 option，并追加唯一 tie-breaker。COUNT 与列表默认不保证同一快照；确有一致性需求时业务选择隔离级别。具体边界见 [CRUD 配套工具](../database/ent/README.md#crud-配套工具)。

ForUpdate/ForShare 或自定义 SQL modifier 查询必须直接执行原生 builder，再组装 Page/Slice；当前 Ent Clone 不保留这些设置，不能传给分页辅助函数。示例 PATCH 保留事务内的直接行锁查询。

`entdb.ErrorClassifier` 接收业务生成包的错误判定函数，区分不存在、校验、约束及明确识别的唯一冲突。框架不解析错误文本、不绑定 HTTP 响应；取消/超时优先。Service/HTTP 显式处理业务错误并保留底层原因，未知数据库错误不自动公开。

## 审计、输出和迁移

Product 使用 IDMixin + AuditMixin；ProductEvent 使用 IDMixin + CreatedMixin，仅存创建元数据。NewAuditHook 在根客户端一次注册，通过生成 mutation 接口识别标准 Mixin，默认从 Principal context 自动填充操作者 ID、名称和 UTC 毫秒时间，无需配置表名。NewIDHook 是独立 ID 策略；不同策略可按实体装配。显式 AuditFull/AuditCreateOnly 与 NewMutationHook 适用于已知字段结构。CreatedMixin 不禁止整行更新/删除，事件不可变策略由业务落实。

Hook 写入 UTC 毫秒时间与完整 actor kind/ID。身份由业务显式传入，认证服务可用 `database/audit/principal.FromContext`；系统任务显式提供 system actor。行上的审计字段不等于操作日志或审计历史，历史事件由业务决定和写入。Hook 最后注册于 runtime hooks，后续 schema hook 不覆盖系统字段。原生 SQL 和 upsert 冲突更新不承诺自动审计。CreateBulk 按 mutation 填充；事务客户端继承启动时装配的 hooks，数据与元数据一起提交/回滚。

响应显式转换为 `respond.MillisTime`，ID 为十进制字符串；不要直接序列化生成实体并依赖未来字段永远不变。

Schema 变更和数据库迁移是两个步骤。初始版本化 SQL 在 [migrations](../examples/ent/migrations)，由业务迁移工具记录和应用；后续可采用 Atlas 生成增量迁移，审查后在部署阶段执行。服务启动不自动调用 Schema.Create。MySQL/PostgreSQL 虽共享 Schema，具体列类型、约束和迁移仍需要各自验证。

[Ent 适配能力与限制](../database/ent/README.md) · [完整商品示例](../examples/ent/README.md)
