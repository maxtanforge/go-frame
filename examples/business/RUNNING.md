# 运行分类业务服务

本例把业务规则、HTTP、Ent 仓储和启动生命周期串联起来。PostgreSQL 为默认推荐；业务通过 DATABASE_DRIVER / DATABASE_URL 选择数据库。本例已接入 postgres/mysql 两种适配器，其他数据库由业务选择相应 connector、Ent 方言和迁移，不由框架猜测 URL。

框架的通用配置只校验驱动名非空，不限制数据库种类，不解析 URL。本例 bootstrap 的适配器选择范围属于该业务应用。

## 目录

```text
cmd/api/                       进程入口
internal/bootstrap/            配置、认证、资源、HTTP 与生命周期
internal/category/             纯业务值、模型、Service、Catalog 和仓储契约
internal/category/http/        DTO、公开字段、错误映射、五个路由与审计顺序
internal/category/store/       Ent 查询与显式事务
internal/ent/schema/            存储模型与共享值校验
internal/ent/                   生成客户端
migrations/<driver>/            显式审查、执行的版本化 SQL
```

业务按功能组织，无需复制框架的能力包目录。启动层创建资源，适配器借用连接池，业务通过自己定义的最小仓储契约运行。

## 准备数据库和身份

从仓库根目录执行；使用专用演示数据库。先由维护者应用对应迁移，本例启动不自动建表：

```powershell
# psql 使用你配置的 PGHOST/PGPORT/PGUSER/PGDATABASE 与认证方式。
psql -v ON_ERROR_STOP=1 -f examples/business/migrations/postgres/202610100001_initial.sql
$env:DATABASE_DRIVER = 'postgres'
$env:DATABASE_URL = 'postgres://example:password@127.0.0.1:5432/example?sslmode=disable'
$env:ID_NODE = '0'
$env:LISTEN_HOST = '127.0.0.1'
$env:HTTP_PORT = '3000'
```

连接 URL 为占位示例，替换为实际配置；sslmode=disable 仅用于本地演示。每个运行实例的 ID_NODE 必须独立分配（0–1023）。选用 MySQL 时显式设置 DATABASE_DRIVER=mysql、匹配的原生 DSN，并执行 mysql 目录迁移（要求支持 CHECK 的 MySQL 版本）；不沿用 PG 迁移。

账本是既有业务资源，本切片不提供账本 CRUD。用已有业务流程准备账本，或在演示数据库执行以下 SQL；Owner 必须匹配调用方已验证的主体 ID：

```sql
INSERT INTO business_books (id, owner) VALUES (1, 'operator-42');
```

认证采用现有 auth/token 的 ES256 JWT + dir/A256GCM JWE profile。运行时显式提供：

| 环境变量 | 内容 |
| --- | --- |
| BUSINESS_AUTH_ISSUER / BUSINESS_AUTH_AUDIENCE | 与签发方一致的 issuer/audience |
| BUSINESS_AUTH_SIGNING_KEY_ID / BUSINESS_AUTH_ENCRYPTION_KEY_ID | 对应的两个 key ID |
| BUSINESS_AUTH_PUBLIC_KEY_FILE | P-256 PKIX PUBLIC KEY PEM 文件 |
| BUSINESS_AUTH_ENCRYPTION_KEY | 32 字节非零随机密钥，RawStdEncoding Base64 |

可复用 [Ent 示例的本地测试令牌步骤](../ent/README.md#认证配置与本地测试令牌)，将环境变量前缀 ENT_AUTH_ 改为 BUSINESS_AUTH_，保持 issuer/audience 与签发配置一致，并使用主体 operator-42。服务没有登录接口或内置密钥，不签发任意用户身份。

## 启动和 CRUD

```powershell
go generate ./examples/business/internal/ent
go run ./examples/business/cmd/api
```

数据库先打开并 Ping，随后构造 Ent store、Catalog、路由及认证政策，全部成功后才监听。资源由 app/resource 管理，退出时排空 HTTP 后关闭连接池。健康 GET/HEAD /healthz 与 /readyz 明确公开，以下五个入口要求有效 Bearer token：

| 方法与路径 | 语义 |
| --- | --- |
| POST /books/{bookId}/categories | 正文只有 name；检查父账本所有权后创建 |
| GET /books/{bookId}/categories | current 默认 1、size 默认 20，最多 100，固定 ID 升序 |
| GET /categories/{id} | 分类详情与所有权检查 |
| PATCH /categories/{id} | name/note 白名单，系统分类不可修改 |
| DELETE /categories/{id} | 所有权检查，系统分类不可删除，返回 deleted=true |

另一个终端中使用签发方提供的 `$TOKEN`：

```powershell
$headers = @{ Authorization = "Bearer $TOKEN" }
$created = Invoke-RestMethod -Method Post -Uri http://127.0.0.1:3000/books/1/categories -Headers $headers -ContentType application/json -Body '{"name":"Food"}'
$categoryID = $created.data.id
Invoke-RestMethod -Uri http://127.0.0.1:3000/books/1/categories?current=1 -Headers $headers
Invoke-RestMethod -Uri "http://127.0.0.1:3000/categories/$categoryID" -Headers $headers
Invoke-RestMethod -Method Patch -Uri "http://127.0.0.1:3000/categories/$categoryID" -Headers $headers -ContentType application/json -Body '{"note":null}'
Invoke-RestMethod -Method Delete -Uri "http://127.0.0.1:3000/categories/$categoryID" -Headers $headers
```

所有响应沿用六字段信封，列表 data 为 current/size/total/pages/records。公开分类只有字符串 id、name、note，不暴露 Owner/ParentID/System。PATCH note 省略保持、null 清除、空字符串存为空值。

创建在事务内锁定父账本；更新和删除在事务内锁定分类，然后检查权限/状态并写入。HTTP 创建审计先设置父账本，再读正文，创建成功才切换到分类；操作日志不是数据库持久审计。Count 与列表在默认 ReadCommitted 下可能观察不同提交，不承诺同一快照。

## 验证与独立消费

```powershell
go test ./examples/business/... -count=1
go run ./scripts/consumer --repo . --mode workspace
```

consumer 将本例复制到仓库外、调整自身导入为独立 module，执行 tidy/test/vet/build；本地 replace 不证明远程模块读取权限。复制为业务工程时修改 examples/business 的模块导入前缀，并按所选框架提交锁定依赖，再生成 Ent。入口写法对照中的 TypedRoutes 仅位于 typed_test.go，测试时依赖 examples/contracts 的实验助手；实际服务使用既有 Routes/Bind/binding 主路径，不将实验作为正式框架 API。

普通测试使用 sqlmock，无需数据库。可选真实 PG 测试仅在显式设置 FRAME_BUSINESS_TEST_POSTGRES_DSN 时执行：

```powershell
go test ./examples/business/internal/category/store -run '^TestPostgresIntegration$' -count=1 -v
# 仓库验收入口（缺少连接配置时明确失败）
./scripts/integration.ps1 -Business
```

它在单个连接的 pg_temp 中应用本迁移的临时表版本并验证 CRUD/回滚，关闭会话后移除临时数据；未提供连接信息会 SKIP，不能记为实库通过。MySQL 的迁移及生产部署环境还需使用方实库验收。
