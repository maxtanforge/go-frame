# Ent 商品业务示例

完整演示 Schema → 生成客户端 → 业务服务 → HTTP。数据库模型在 `ent/schema`，字段值规则在 `internal/productvalue`，请求 DTO 在 `model.go`；不再手工维护持久化实体和列名更新 map。

普通业务入口优先参考 [http.go](http.go) 中 binding 的有类型回调：输入与字段规则在注册时准备，五个 CRUD 入口通过 `Route.Bind` / `router.Collect` 一次收集，构造失败携带模块、方法、路径和原始原因，全部成功后才挂载。回调不重复 Validate；服务仍保护业务状态和事务，HTTP 响应通过显式转换选择公开字段。Register 保留现有 chi 装配行为；声明中的鉴权与操作政策由 NewAuthenticated 消费，不由普通 Method 挂载执行。

本示例为展示 Ent 接入，服务可以直接操作业务生成实体；消费工程需要不同业务模型时，再在所属 feature 内区分 Entity、值对象、命令和 DTO，不要求逐表复制结构。目录与 PATCH 转换规则见[建模指南](../../docs/modeling.md)，显式路由表与业务测试边界见[业务接入说明](../../docs/business-integration.md)。

五个 CRUD 入口在 binding 选项中复用 `MapError: httpError`，回调直接返回业务错误。解码 / 字段校验错误不被模块 mapper 改写；取消与 deadline 保留，未知错误由标准响应边界安全处理。mapper 应保留原始 cause，框架也会防止映射吞掉错误或丢失 cause。

## 启动

先在业务数据库通过你的迁移工具执行所选方言的 `migrations/<mysql|postgres>/202609300001_initial.sql`，记录执行版本。它是初始建表迁移，不幂等，不应每次启动重复运行。框架不自动执行 SQL。事件 action 在 MySQL 使用 ENUM，PostgreSQL 使用 CHECK，均限制为 created、updated、deleted；Schema 和版本化 SQL 必须共同审查。

从仓库根目录：

```powershell
go generate ./examples/ent/ent
$env:DATABASE_DRIVER = 'postgres'
$env:DATABASE_URL = 'postgres://user:password@localhost:5432/frame?sslmode=disable'
$env:ID_NODE = '0'
# Set ENT_AUTH_* using the local test-token instructions below or your issuer.
go run ./examples/ent
```

默认推荐 PostgreSQL，先执行 postgres 目录迁移；上述 sslmode=disable 仅用于本地演示环境。每个 ID 生成器实例的 ID_NODE 必须独立分配（0–1023）。生产业务只导入所选数据库的连接包；本例为了演示，显式支持两个方言。业务不能启用 Ent Debug 打印敏感 SQL。

```sh
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"Tea","price":199,"active":true,"note":"green"}' localhost:3000/products
curl -H "Authorization: Bearer $TOKEN" 'localhost:3000/products?current=1&size=20'
curl -H "Authorization: Bearer $TOKEN" 'localhost:3000/products?current=1&size=20&active=false&sort=price&desc=true'
curl -H "Authorization: Bearer $TOKEN" localhost:3000/products/REPLACE_ID
curl -X PATCH -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"price":0,"active":false,"note":null}' localhost:3000/products/REPLACE_ID
curl -X DELETE -H "Authorization: Bearer $TOKEN" localhost:3000/products/REPLACE_ID
curl -H "Authorization: Bearer $TOKEN" localhost:3000/metrics
```

上述 shell 示例的 `TOKEN` 是调用方持有的测试凭证。PowerShell 的双终端调用方式见下文，使用 `$TOKEN` 和 `curl.exe`。缺失或无效 Bearer 返回 401，DTO 错误返回 400，两者都不会开启业务事务。

## 认证配置与本地测试令牌

入口显式装配 `router.NewAuthenticated`、操作日志 recorder 和请求指标 recorder。公开白名单只有 GET/HEAD `/healthz`、`/readyz`，公开请求忽略凭证；所有商品接口和后来新增的端点默认要求认证。`Register` 继续拥有现有 CRUD 处理器，不复制业务逻辑。认证后的 `principal` 通过 `auditprincipal.FromContext` 写入商品和事件的 subject 审计字段，操作日志同时记录 actor ID/issuer。独立 Service/sqlmock 测试仍显式注入 system actor `ent-example`。

示例配置由 `auth_config.go` 拥有，必须提供以下环境变量，在数据库资源启动前检查，不使用内置密钥：

| 变量 | 值 |
| --- | --- |
| `ENT_AUTH_ISSUER` / `ENT_AUTH_AUDIENCE` | 与签发方一致的非空 issuer/audience |
| `ENT_AUTH_SIGNING_KEY_ID` / `ENT_AUTH_ENCRYPTION_KEY_ID` | 与签发方一致的两个 key ID |
| `ENT_AUTH_PUBLIC_KEY_FILE` | 一个 P-256 PKIX `PUBLIC KEY` PEM 文件路径 |
| `ENT_AUTH_ENCRYPTION_KEY` | 32 字节非零随机密钥的无填充标准 Base64（`base64.RawStdEncoding`） |

令牌使用现有 `auth/token` 的 ES256 JWT + dir/A256GCM JWE profile，不接受任意第三方 JWT。真实业务的签发、密钥保管/轮换、权限和租户规则由业务拥有；本例没有登录接口。

本地测试可从仓库根目录运行以下 PowerShell。它通过现有 `token.NewIssuer`/`Issue` 生成 15 分钟令牌和全新的临时测试密钥，私有签名密钥仅保留在辅助进程内。不要把这些材料用作生产配置或提交到仓库：

```powershell
$tokenSource = Join-Path $env:TEMP 'frame-ent-test-token.go'
@'
package main
import (
    "context"
    "crypto/ecdsa"
    "crypto/elliptic"
    "crypto/rand"
    "crypto/x509"
    "encoding/base64"
    "encoding/json"
    "encoding/pem"
    "os"
    "github.com/maxtanforge/go-frame/auth/principal"
    "github.com/maxtanforge/go-frame/auth/token"
)
func check(err error) { if err != nil { panic(err) } }
func main() {
    signing, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader); check(err)
    encryption := make([]byte, 32)
    _, err = rand.Read(encryption); check(err)
    public, err := x509.MarshalPKIXPublicKey(&signing.PublicKey); check(err)
    file, err := os.CreateTemp("", "frame-ent-public-*.pem"); check(err)
    check(pem.Encode(file, &pem.Block{Type: "PUBLIC KEY", Bytes: public}))
    check(file.Close())
    issuer, err := token.NewIssuer(token.IssuerOptions{
        Issuer: "local-ent-test", Audience: "products", SigningKeyID: "test-signing",
        EncryptionKeyID: "test-encryption", SigningKey: signing, EncryptionKey: encryption,
    }); check(err)
    credential, err := issuer.Issue(context.Background(), principal.Principal{ID: "operator-42"}); check(err)
    check(json.NewEncoder(os.Stdout).Encode(map[string]string{
        "public_key_file": file.Name(), "encryption_key": base64.RawStdEncoding.EncodeToString(encryption),
        "access_token": credential.AccessToken,
    }))
}
'@ | Set-Content -LiteralPath $tokenSource -Encoding utf8
$testAuth = go run $tokenSource | ConvertFrom-Json
$env:ENT_AUTH_ISSUER = 'local-ent-test'
$env:ENT_AUTH_AUDIENCE = 'products'
$env:ENT_AUTH_SIGNING_KEY_ID = 'test-signing'
$env:ENT_AUTH_ENCRYPTION_KEY_ID = 'test-encryption'
$env:ENT_AUTH_PUBLIC_KEY_FILE = $testAuth.public_key_file
$env:ENT_AUTH_ENCRYPTION_KEY = $testAuth.encryption_key
# Before starting the service, copy this token into the second terminal's TOKEN variable.
$testAuth.access_token
```

先将第一终端显示的令牌复制到第二终端的内存变量：

```powershell
$TOKEN = '复制本次生成的 access_token'
```

在第一终端按上文配置 DATABASE_DRIVER、DATABASE_URL 和 ID_NODE，然后启动服务。该命令阻塞并保持运行：

```powershell
go run ./examples/ent
```

在第二终端调用接口或抓取指标（不要将令牌写入文件、Git 或全局环境设置）：

```powershell
curl.exe -H "Authorization: Bearer $TOKEN" localhost:3000/products
curl.exe -H "Authorization: Bearer $TOKEN" localhost:3000/metrics
```

每次重新生成密钥后重启服务加载对应验证材料，令牌过期后重新生成。辅助程序、临时公开密钥和 shell 环境变量由调用方清理。生产部署通过你的秘密管理系统注入配置。

`composition.go` 将完成事件转换成 `metric.Observation{Route, Method, DurationMS}`，由 recorder 同时维护本地直方图和 `OnObserve(ctx, observation)` 出口；综合测试演示回调注入，业务可在构造 recorder 时接入 OTel 适配器。示例显式注册 GET `/metrics` 输出 Prometheus 文本，抓取需要 Bearer 认证，抓取请求本身也记录指标；不额外监听指标端口或安装全局 provider。拒绝认证时路由尚未进入 chi 处理器，指标归入 `<unmatched>`；操作记录使用认证路由探测结果，保留可信模板。日志和指标不记录 Authorization、令牌或请求正文；操作日志描述 HTTP 结果，不代替事务提交证明。

## 行为约定

Product 使用 IDMixin + AuditMixin，ProductEvent 使用 IDMixin + CreatedMixin，只保留创建时间和创建人。两者都使用雪花 ID。示例入口通过 InstallHooks 显式装配完整 Product Hook，以及 Event 的独立 ID Hook + AuditCreateOnly Hook；事件是业务审计历史，和行上的 created/updated 字段职责不同。事件表不建立商品外键，因此硬删除仍保留历史。CreatedMixin 不自动禁止整行更新或删除；本例事件写入由服务控制。

HTTP 使用 binding.Query/JSON/Path/JSONWithPath，注册时编译 DTO、默认值和 v 规则；列表分页使用 d 缺失默认值，路径参数由 chi 显式提取。自定义 Validate(*validation.Context) 通过 validation.Check 调用公共规则，收集完整诊断；HTTP 仅展示首条安全消息和 null data。创建/PATCH DTO 只允许业务字段；严格解码拒绝 id、created_at 等系统字段。DTO 与 Schema 复用 productvalue 的规则，名称/备注长度按 UTF-8 字节计（与 Ent MaxLen 一致），拒绝 NUL 和非法 UTF-8，价格为非负最小货币单位整数。公共规则返回相对路径，不绑定某个接口字段名；Ent 校验仍可能在首个失败处结束，完整输入诊断由 DTO 层提供。接口规则仍区分创建必填和 PATCH 缺省，不从数据库 nullable 直接推导权限。

PATCH 未传不修改，price:0、active:false 明确写入；note:null 通过 nullable:"true" 明确允许并清空，note:"" 保存空字符串；其他字段 null 和空 PATCH 拒绝。原生 Set/Clear 方法清楚表达这些差异。修改前锁行，创建/修改/删除及事件共享原生 Ent 事务，事件失败整体回滚。

分页使用唯一 id ASC，返回 current/size/total/pages/records，空集合 []。COUNT 与读取是两个语句，不承诺并发下同一快照。响应显式映射白名单：ID 字符串、毫秒时间，不能直接返回 Ent 实体把未来新增内部字段暴露出去。

## CRUD 辅助接线

入口调用 NewService，在启动期编译 id/name/price 排序白名单，最后注册 HTTP 处理器。列表支持 active 过滤（false 与缺失不同），sort 默认 id，desc 默认 false；未知排序字段返回 400。相同价格/名称下追加 id 升序，保证唯一排序。Count 和列表来自同一个过滤查询的独立 clone；QueryPage 不修改原 builder，不保证两次查询共享数据库快照。

创建、PATCH 和删除使用 entdb.WithTx，商品与事件仍写入同一显式 Ent 事务。业务自行 Set/Clear，不推导权限。ErrorClassifier 接入生成包判定函数，不存在时映射为业务 ErrNotFound 并保留原始原因；其他数据库错误保持内部错误，不把全部约束失败解释成重复记录。辅助函数详细约定见 [database/ent](../../database/ent/README.md)。

## 生成与迁移维护

`go generate ./examples/ent/ent` 使用 go.mod 锁定的 `go tool ent`，包括 sql/lock 特性。将 Schema、生成客户端和版本化 SQL 一起审查提交，不手改生成文件。变更 Schema 不等于数据库已更新；增量迁移由业务的迁移工具（可用 Atlas）生成/审查/执行。本例没有迁移执行器或自动生产建表。

业务项目初始化工具：`go get -tool entgo.io/ent/cmd/ent@v0.14.6`，在自己的 ent/generate.go 写入同样的 go:generate 指令，然后 `go generate ./ent`。工具和运行时使用同一模块版本；不要混用全局 ent 命令。完整约定见 [建模指南](../../docs/modeling.md)。

## 验证

```sh
go test ./database/ent ./examples/ent -count=1
go test ./examples/ent -run '^TestIntegrationEnt$' -count=1 -v
```

普通测试使用 sqlmock，不监听端口，覆盖生成客户端的 CreateBulk、事务提交/回滚、独立 ID Hook 与 runtime Hook 顺序。PostgreSQL SQL 生成测试不等于真实 PostgreSQL 连接/事务验证。实库测试显式读取 FRAME_TEST_MYSQL_DSN / FRAME_TEST_POSTGRES_DSN，未配置则 SKIP，不等于通过。MySQL 接受原生 DSN（parseTime=true&loc=UTC）或无查询参数的 mysql:// URL，TLS 等选项使用原生 DSN。

实库测试固定一条物理连接，将初始迁移转换为会话临时表，PostgreSQL 限制 search_path=pg_temp；连接断开也不会转到持久表。仅在临时事件表设置唯一约束触发真实回滚，覆盖 CRUD、零值/null、分页、时间精度与取消。连接值不写入日志或仓库。
