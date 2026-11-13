# Ent 数据访问适配

包路径 `github.com/maxtanforge/go-frame/database/ent`，包名 **entdb**。Ent 是 Frame 推荐的可选 ORM。核心 database 不依赖 Ent，业务直接使用 Ent 生成的类型安全客户端；Frame 提供分页、事务、排序和错误分类辅助，不封装通用 Repository。

## 连接与生命周期

```go
drv, err := entdb.New(pool.SQLDB(), entdb.Options{
    Dialect: "mysql", // PostgreSQL 使用 "postgres"
    Logger: appLogger,
    SlowThreshold: 200 * time.Millisecond,
})
if err != nil { return err }
client := ent.NewClient(ent.Driver(drv)) // ent 来自业务项目生成目录
```

`New` 不执行 I/O、不 Ping、不迁移、不调整池配置。仅接受 mysql/postgres；包本身不导入两者的 SQL 驱动，业务选择对应的池适配器。`Driver.Close`（以及经其调用的 `client.Close`）是空操作，不关闭借用池；`pool.Close` 由业务生命周期所有者执行。关闭适配器后也不会改变池或使客户端失效。

`Tx` 和 `BeginTx` 保留原生事务及隔离选项；事务中使用 `tx.Client()` 或 `tx.Product` 等对象，不能混用根 client 或 Frame SQL 事务。事务实体在提交后需要继续使用其实体方法时，按 Ent 约定 `Unwrap()`。示例中的业务事务辅助函数会合并回滚错误，不吞掉原始失败。

日志默认只记录慢操作和错误；纯请求取消按 DEBUG 记录，默认日志级别下不输出，其他失败按 ERROR 记录；阈值零表示 200ms，负值拒绝。记录固定操作名、`duration_ms`、`failed`、`canceled`，不输出 SQL、参数、原始错误。请求上下文 logger 优先，随后为 Options.Logger 和 slog 默认 logger。Query 耗时截止到返回 Rows，不包含后续遍历/Scan；事务开始、提交和回滚单独计时。原始错误仍返回业务，业务负责公开错误映射。

**不要在生产开启 `client.Debug()` 或 `ent.Debug()`**：它们在适配器之外直接记录 SQL/参数，会绕过这里的日志隐私约定。

## CRUD 配套工具

### 分页执行

```go
window, err := pagination.NewWindow(current, size, pagination.DefaultLimits())
if err != nil { return err }
query := client.Product.Query().Where(product.ActiveEQ(true)).Order(product.ByID())
page, err := entdb.QueryPage(ctx, query, window)
// page 的类型由查询推导为 pagination.Page[*ent.Product]。
// 不计总数时：slice, err := entdb.QuerySlice(ctx, query, window)
```

QueryPage 从同一个过滤基线 Clone 两个独立 builder，依次执行 Count 和列表，保留原始查询不变。QuerySlice 不执行 Count，读取 size+1 行判断 has_more。Page 保持 current/size/total/pages/records 五字段；无记录为 []。参数、平台 int 范围、取消与执行错误均检查，不吞错或返回伪造总数。

传入查询必须尚未调用 Limit/Offset；不要传入改变计数语义的聚合/投影。调用方明确设置唯一稳定排序，并保证执行期间不并发修改查询或谓词捕获的状态。Clone 不保证两条 SQL 共享快照，Count 为零时仍执行列表，以保留默认隔离级别下后续新增记录的可能性；一致性要求由显式事务和隔离级别决定。分页范围限制继续由 Window 管理。

**锁查询不适用这两个辅助函数。** 当前锁定的 Ent 版本所生成的 Clone 不复制 SQL modifiers，ForUpdate/ForShare 会丢失。需要行锁时，在显式事务内直接调用原生 builder 的 Offset/Limit/ForUpdate（或 ForShare）/All，随后用 pagination.NewPage/NewSlice 组装结果；不要将已加锁或自定义 modifier 的查询传入 QueryPage/QuerySlice。商品 PATCH 示例沿用直接 ForUpdate 查询。升级 Ent 时运行对应生成查询回归测试并重新核对该边界。

### 排序白名单

```go
sorter, err := entdb.NewSorter(map[string]entdb.SortOptions[product.OrderOption]{
    "id": {Asc: product.ByID(), Desc: product.ByID(sql.OrderDesc())},
    "price": {Asc: product.ByPrice(), Desc: product.ByPrice(sql.OrderDesc())},
}, []pagination.Sort{{Field: "id"}})
if err != nil { return err }
// sql 是 entgo.io/ent/dialect/sql；启动时创建 sorter，随后复用。
order, err := sorter.Resolve([]pagination.Sort{{Field: "price", Desc: true}})
if err != nil { return err }
query := client.Product.Query().Order(order...)
```

结果为 price 降序后 id 升序；显式请求 id 降序时保留请求方向，不重复追加 id。请求别名未知、重复或最终超过八个排序项返回 pagination.ErrInvalid；配置无效返回 ErrSortConfig。必须提供至少一个 tie-breaker，其唯一性由业务保证。映射和切片在构造时复制；option 函数及捕获状态由业务保证只读并发安全，不接受用户 SQL。示例只允许 id/name/price，DTO 默认 sort=id、desc=false。

### 显式事务

```go
err := entdb.WithTx(ctx, client.Tx, func(tx *ent.Tx) error {
    // 全部读写使用 tx.Product / tx.Client()。
    return tx.Product.Create().SetName(name).SetPrice(price).Exec(ctx)
})
```

多次读写时逐项返回错误，完整代码见商品示例。WithTx 的 begin 参数也可以是调用 client.BeginTx(ctx, options) 的闭包，以显式选择隔离级别。辅助函数保留具体生成 Tx 类型，不往 context 注入客户端，不自动嵌套事务、不重试。

回调返回错误或 context 取消则回滚；两者同时发生时均保留，回滚错误通过 errors.Join 与原错误合并。panic/Goexit 也执行回滚，panic 原样传播（异常退出时无法通过函数返回回滚错误）。回调成功且 context 有效才提交；只有 Commit 返回 nil 才解除清理，因为 Ent 的 OnCommit 可以在真正提交前失败。提交失败或提交钩子异常退出仍尝试回滚，保留提交错误，不推测数据库最终结果、不自动重试。清理时只忽略最终指向 sql.ErrTxDone 的单条包装链；复合错误完整保留，避免丢失独立的回滚钩子失败。回调不得自行提交/回滚；提交/回滚钩子必须遵守 Ent 中间件协议，不吞掉底层结果、不阻止必要清理；写入后取出的实体在事务结束后使用其实体方法仍需显式 Unwrap。

### 错误分类

```go
classifier := entdb.ErrorClassifier{
    IsNotFound: ent.IsNotFound,
    IsConstraint: ent.IsConstraintError,
    IsValidation: ent.IsValidationError,
}
kind := classifier.Classify(err)
```

ent 为业务生成包。分类结果包括 ErrorNone、ErrorUnknown、ErrorNotFound、ErrorConstraint、ErrorValidation、ErrorUniqueConstraint、ErrorCanceled、ErrorDeadlineExceeded。context 错误优先于配置的判定函数；nil 函数跳过。不会转换/替换原错误或解析错误消息。业务仍需保留原 error，选择公开信息和 HTTP 状态。

Constraint 包括外键等约束，不等于重复数据；只有显式提供 IsUniqueConstraint（检查方言的结构化错误码等）才返回唯一冲突类别。不要把所有 Constraint 映射成“名称已存在”，也不要公开原始 SQL/约束名。数据库唯一约束仍是最终保障，提前 Exists 查询不能避免并发竞争。

## 公共模型与审计

Schema 组合：

```go
func (Product) Mixin() []ent.Mixin {
    return []ent.Mixin{entdb.IDMixin{}, entdb.AuditMixin{}}
}
```

- IDMixin：正 int64、不可变、应用生成，不启用数据库自增；JSON 使用十进制字符串。
- AuditMixin：created_at/updated_at 使用领域 `time.Time`，MySQL DATETIME(3)、PostgreSQL TIMESTAMPTZ(3)；created_by/updated_by 与对应 `_kind` 存储完整 actor 语义。创建字段不可变。对外毫秒时间通过响应 DTO 的 `respond.MillisTime` 映射。

启动时一次注册审计 hook，无需配置业务表名。它通过生成 mutation 的类型接口识别完整的 AuditMixin（八字段）或 CreatedMixin（四字段），其他 Schema 原样通过。字段类型和命名采用 Mixin 的固定约定；自定义结构可显式选择 Profile 或提供自己的 hook。

```go
auditHook, err := entdb.NewAuditHook(entdb.AuditOptions{})
if err != nil { return err }
client.Use(auditHook)
// ID 是独立策略；只有全部实体都使用 int64 IDMixin 时才全局注册。
idHook, err := entdb.NewIDHook(snowflake)
if err != nil { return err }
client.Use(idHook)
```

Actor 默认从可信 Principal context 取得 ID 和 Name；任务显式注入 system actor resolver。缺少身份不降为匿名或系统。依赖必须并发安全，完成注册后才能并发使用客户端。审计字段还包含 created_by_name/updated_by_name，保存操作时的名称快照；名称不用于鉴权。不同 ID 策略可按实体注册 NewIDHook，审计仍只注册一次。

创建总是生成新 ID 并覆盖系统字段；更新保留创建字段，重写更新时间和操作者，批量更新同样适用。删除不调用 ID/Actor 依赖，删除事件由业务另行写入。时间截断为 UTC 毫秒，不读取旧行，因此不保证时钟回退时的历史单调性，也不提供并发版本控制。Hook 不是操作历史表、权限规则或防越权机制。

Hook 应最后注册于 runtime hooks；Ent 的 schema hooks 在它之后运行，不能再改系统字段。原生 SQL、其他客户端和显式停用 Hook 均可绕过此约定。Ent upsert 的冲突更新路径并不执行普通更新 hook，不能依赖它保存创建元数据或自动更新 updated_*；必须另行明确 SQL 冲突更新字段与审计策略。没有安装 Hook 时必填系统字段不会被自动补齐。

### 仅创建审计与独立 Hook

追加型事件表可组合 IDMixin + CreatedMixin，只声明 created_at、created_by、created_by_kind、created_by_name。不要再同时组合 AuditMixin，以免字段重名。CreatedMixin 仅令创建元数据不可变，业务字段仍可更新；禁止修改/删除整行应使用业务权限或独立策略。

```go
idHook, err := entdb.NewIDHook(snowflake)
if err != nil { return err }
createdHook, err := entdb.NewAuditHook(entdb.AuditOptions{
    Profile: entdb.AuditCreateOnly,
    Actor: auditprincipal.FromContext,
})
if err != nil { return err }
if err := entdb.InstallHooks([]ent.Hook{idHook, createdHook}, client.ProductEvent); err != nil {
    return err
}
```

NewAuditHook 的零 Profile 自动识别标准生成接口；显式 AuditFull/AuditCreateOnly 适用于已知字段结构，未知 Profile 在启动时返回错误。仅创建模式更新时清除 created_* 修改，不读取身份或时钟，不写 updated_*。NewMutationHook 是 ID + 显式完整审计的组合入口，只能安装在匹配实体上。

CreateBulk 为每个创建 mutation 执行 hook，事务客户端沿用注册的 hooks。应先在根实体客户端装配，再创建事务；元数据通过同一 mutation 写入同一事务，回滚随业务行回滚，已消耗的 ID 可以有空洞。不要在 hook 中另开事务或异步写审计。后续 schema hook 不得覆盖系统字段。行审计仅记录创建与最后修改，硬删除后不保存本行历史。

## 生成与迁移

在业务仓库维护 Schema 和生成代码，见 [建模指南](../../docs/modeling.md) 与 [完整示例](../../examples/ent/README.md)。使用官方 Ent 工具，生成版本与运行时一起由业务 go.mod 锁定；生成文件提交版本库，不手改。表迁移由业务审查和执行，框架服务启动不调用 `Schema.Create`。
