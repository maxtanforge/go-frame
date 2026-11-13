# SQL 数据库

`database` 只依赖 Go 标准库，负责连接池、探活、关闭和事务。业务服务按需选择驱动适配包，两个适配器都返回 `*database.DB`：

示例默认推荐 PostgreSQL；`DATABASE_DRIVER` 和 `DATABASE_URL` 由业务按实际数据库配置，驱动适配器与 DSN 必须对应。

```go
import (
    "github.com/maxtanforge/go-frame/database"
    "github.com/maxtanforge/go-frame/database/postgres"
)

db, err := postgres.Open(postgres.Options{
    DSN: connectionString,
    Pool: database.Options{MaxOpenConns: 20, MaxIdleConns: 5},
})
```

MySQL 导入 `database/mysql`，调用 `mysql.Open(mysql.Options{DSN: ..., Pool: ...})`。构造阶段只解析配置，不连接数据库；启动时使用带超时的 `db.Ping(ctx)` 验证连通性，并让应用资源生命周期管理 `db.Close()`。`SQLDB()` 返回借用的 `*sql.DB`，用于 SQL 查询或业务自选 ORM，调用方不要自行关闭。

自定义 TLS、凭据提供器等高级场景可直接配置驱动的 `driver.Connector`，再调用 `database.Open(connector, poolOptions)`。成功构造后连接池归 DB 所有；参数校验失败时框架不会关闭调用方的 connector。Close 幂等，重复调用返回同一关闭结果。

`database.Executor` 是 `*sql.DB` 与 `*sql.Tx` 共用的查询接口，包含 ExecContext、QueryContext 和 QueryRowContext。业务仓储显式接收它，事务内的多个操作始终传递同一个执行器。可选 [frame 实例入口](../docs/architecture.md) 借用具名 DB，以 `WithTx` 向业务回调提供事务执行器；连接的创建和关闭仍由启动层负责，见 [frame 消费示例](../examples/transactions/README.md)。

## 可选 Ent

推荐需要 Schema 建模和类型安全 CRUD 的业务接入 [database/ent](ent/README.md)。
`entdb.New(db.SQLDB(), entdb.Options{Dialect: "postgres"})` 只借用现有池，
不导入具体数据库驱动、不 Ping、不自动迁移；客户端使用业务项目自己的生成代码。
`Driver.Close` 不关闭借用池，真正的关闭仍由本包的池所有者完成。
原生 Ent 事务与本包 SQL 事务不会自动相互加入，事务中必须始终使用同一事务客户端。
完整接线见 [Ent 商品样例](../examples/ent/README.md)。

## 驱动差异

- MySQL 使用 go-sql-driver/mysql 的 DSN 格式和 `?` 参数占位符。
- PostgreSQL 使用 pgx 的标准 `database/sql` 适配，支持 PostgreSQL URL 和 keyword DSN，参数占位符为 `$1`、`$2`。未在 DSN 指定的配置遵循 pgx 默认规则，包括 PG 环境变量、service 文件和密码文件；部署时显式管理这些配置来源及 TLS 设置。适配器要求非空 DSN。
- 框架不改写 SQL，也不统一方言、数据类型、隔离级别支持或数据库迁移。`WithTx` 负责成功提交、错误/恐慌/协程退出回滚；回调不可自行提交或回滚。
- 构造错误不携带原始 DSN 或驱动解析错误，避免暴露凭据。运行时查询/连接错误保留驱动语义，业务仍需控制日志和对外错误内容。
- 安全 SQL 日志（慢操作与错误脱敏、不输出 SQL 与参数）目前仅 [database/ent](ent/README.md) 的驱动装饰器提供；`SQLDB()` 直连与 `WithTx` 路径不经过任何框架日志装饰。直连业务需要 SQL 审计时自行包装 `driver.Connector`，或接入 Ent；框架暂不提供通用 SQL 日志装饰器（待真实消费者驱动，见路线图）。

默认池上限为 25 个连接，最多 5 个空闲连接（不超过连接上限），空闲寿命 5 分钟，总寿命 30 分钟。零值使用默认值；负数和空闲上限超过总上限会报错。多实例部署时应根据数据库连接预算设置每实例上限。

同一 go.mod 管理依赖版本，按包导入可避免编译另一种数据库驱动，但不等于独立模块依赖图或版本管理。

## 验证

`go test ./database/...` 默认执行无需服务的测试。设置 `FRAME_TEST_MYSQL_DSN` 或 `FRAME_TEST_POSTGRES_DSN` 后，对应适配包会在独立测试库创建唯一命名的临时测试表，验证提交／回滚后的数据可见性、取消和连接池耗尽后的等待超时，最后清理该表。测试用户需要该测试库的 CREATE/DROP 权限，不应使用业务库。见[集成验证说明](../docs/integration-testing.md)。

pgx 连接行为参考 [官方 stdlib 文档](https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib)。
