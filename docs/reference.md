# 能力与使用参考

按包选择能力见[能力目录](packages.md)，接入完整业务见[业务装配](business-integration.md)。

## 能力与边界

| 包 | 业务项目可以复用的能力 |
|---|---|
| `app` | 资源生命周期、命名服务组合、可取消后台任务、排空与逆序清理 |
| `concurrency` | 固定 worker 的有序批处理、进程内并发准入、有界等待与过载拒绝、取消与资源释放 |
| `app/resource` | 通用资源装配：延迟创建、可选探活、带 context 的清理、部分初始化回滚 |
| `http/server` | 标准库 HTTP 服务、TLS、资源限制及生命周期 |
| `http/router` | 可选 chi 路由装配、健康端点、统一 404/405、panic |
| `http/binding` | 声明式 DTO、强类型 Query/JSON/Path/JSONWithPath 处理函数 |
| `http/middleware` | 请求完成日志、路由模板、响应字节、耗时、指标回调、协作 deadline、并发准入 |
| `http/health` | 将 health 报告输出为 HTTP 健康端点 |
| `http/respond`、`http/httperror` | 响应包络、公开 HTTP 错误、毫秒时间类型 |
| `pagination`、`database/query` | 受限 Offset 窗口、统一分页结果、排序白名单及 SQL 方言片段 |
| `database/audit`、`database/audit/principal` | 显式操作人、UTC 毫秒创建/更新字段及校验；操作事件主体转换 |
| `app/command`、`cmd/frame` | 服务 run/version/config 命令分发；开发热重启 |
| `health` | 必需／可选检查、总超时、独立并发池、降级状态、退出就绪门控 |
| `telemetry/trace` | 默认雪花请求关联 ID、上游 ID 校验、context 和响应头传播 |
| `telemetry/logger` | console/JSON slog、受管理的文件轮转、保留/压缩、错误报告、动态级别 |
| `identity` | 服务/版本/环境/实例元数据，跨平台安全的实例日志路径 |
| `lock/redis` | 借用 Redis 客户端的租约锁：持有者比对、可选自动续期、失锁取消业务 context |
| `schedule` | 本地周期任务：毫秒间隔或五字段 Cron（显式时区），默认同任务不重叠、不补跑 |
| `database` | 通用 `database/sql` 连接池、参数校验、事务提交/回滚、底层 `*sql.DB` |
| `database/mysql`、`database/postgres` | 可选驱动适配，DSN 解析与 connector 构建 |
| `database/ent` | Ent 借用池、安全日志、审计 Hook，以及强类型分页、事务、排序和错误分类 |
| `validation/tags` | 可选声明式入参校验，安全字段诊断与 schema 错误区分 |
| `optional` | PATCH 字段缺失、null 和实际值的三态表示 |
| `redis` | 连接池硬上限、context 超时、ACL、TLS、自定义读写超时、固定窗口计数器 |
| `cache`、`cache/memory`、`cache/redis` | 缓存契约、对象读写、本地 LRU＋TTL 和 Redis 适配 |
| `http/client` | 独立连接池、有界超时、关联 ID 传播、JSON 调用及有限响应读取 |
| `telemetry/otel` | 显式 OpenTelemetry HTTP 观测适配、W3C 传播、slog 链路关联 |
| `telemetry/metric`、`telemetry/metric/otel` | 零依赖请求延迟直方图与 Prometheus 文本导出;OTLP 双写为可选绑定 |
| `config` | 业务配置解码、Prepare 结构预检、来源合并后校验及脱敏诊断 |
| `config/loader` | 显式 YAML/dotenv、环境变量快照和 CLI 来源组合 |
| `config/reload` | 配置热重载：快照管理、变更 Diff 与文件 Watch 触发更新 |
| `app/config`、`app/config/redis` | 可选服务配置组件与 Redis TLS 选项映射；SQL 池选项由业务显式组装 |
| `config/dotenv`、`config/yaml` | dotenv 与 YAML 配置来源解析 |
| `http/request` | 严格 JSON/query/path 解码、参数边界和安全字段错误 |
| `validation` | 无协议依赖的公共字段校验契约和固定规则 |
| `auth/principal`、`auth/token`、`auth/http` | 请求身份、加密令牌、公开名单与默认认证 |
| `telemetry/operation`、`telemetry/operation/http` | 类型化操作事件、输出适配与 HTTP 自动记录 |
| `app/startup` | 配置映射与 Run 日志生命周期：启动、错误记录和关闭 |
| `id` | 显式节点雪花 ID、回拨检测、可取消的序列耗尽等待 |

认证可通过 [`auth/http`](../auth/http/README.md) 接入，默认保护业务接口，公开名单按方法和精确路径匹配。
[`auth/token`](../auth/token/README.md) 提供签名后加密的 access token，[`auth/principal`](../auth/principal/README.md) 传递已验证身份；
[`telemetry/operation`](../telemetry/operation/README.md) 自动关联操作人。登录业务、权限、租户、缓存策略和 ORM 由应用决定。

## 业务模块

模块通过 `Register` 显式接收路由器、业务服务及必要的入口依赖；greeting 使用 `Register(chi.Router, Service)`，records 还接收显式的 `audit.Actor`。参考 [`greeting HTTP 层`](../examples/basic/internal/greeting/http.go) 与 [`业务服务`](../examples/basic/internal/greeting/service.go)。

```go
router.Route("/api", func(r chi.Router) {
    r.Use(yourAuthenticationMiddleware)
    orders.Register(r, orderService)
})
```

处理函数只返回数据或错误：

```go
input, err := request.DecodeJSON[CreateOrderRequest](../w, r)
if err != nil { return nil, err }
return orderService.Create(r.Context(), input)
```

`DecodeJSON` 默认限制 1 MiB，拒绝未知字段、空/null 请求体、多段 JSON、错误 Content-Type。分别映射为 400、413、415。`DecodeOptions{MaxBytes: ...}` 可覆盖大小；完整声明式绑定和 `Validate(*validation.Context)` 见 [binding](../http/binding/README.md)，低层 DecodeJSON 不自动执行 d/v 标签。内部错误保留在日志，对外返回稳定文案。不要在验证错误中包含完整输入或密钥。

[认证与操作日志示例](../examples/authenticated/README.md) 使用 recorder 完成整链验证，
展示 `router.NewAuthenticated` 就近声明公开策略、操作描述和审计人转换。

业务模块可用 `Route.Bind` 接住现有 binding 工厂的 `(http.Handler, error)`，
由 `Collect` 一次收集路由，避免逐个拆开 handler 与启动错误：

```go
routes, err := router.Collect("orders",
    router.Route{Method: "GET", Path: "/orders/{id}"}.Bind(
        binding.Path(getOrder, binding.Options{PathParams: func(r *http.Request) map[string]string {
            return map[string]string{"id": chi.URLParam(r, "id")}
        }})),
    router.Route{Method: "POST", Path: "/orders"}.Bind(binding.JSON(createOrder)),
)
if err != nil { return err }
r, err := router.NewAuthenticated(logger, router.AuthenticatedOptions{
    Verifier: verifier,
    Routes: routes,
})
```

`getOrder`、`createOrder` 沿用 binding 的类型化函数签名。`Bind` 的 Route 只填元数据，
已有 Handler 会报错；原生 handler 可用 `.Bind(handler, nil)`。
`Collect` 保留声明顺序，校验 handler、方法、路径、操作元数据、公开路径和重复的
方法/路径；任意失败返回 nil 路由表，错误包含模块、方法和路径，并保留原始错误供
`errors.Is` 检查。模块名非空、最长 128 字节，禁止首尾空白和控制字符。
chi 参数名不同或模式重叠的路由不作为重复声明检测，业务应避免同一方法下声明
`/orders/{id}` 与 `/orders/{slug}` 等等效模式。健康端点冲突和最终挂载由
`NewAuthenticated` 校验；路由校验或 chi 挂载失败会指出具体方法和路径。
默认鉴权、启动目录和响应机制保持不变，公开路由仍只允许规范的字面量路径。

## 响应与错误

```json
{"code":200,"data":null,"message":"成功","success":true,"timestamp":1790000000000,"traceId":"..."}
```

| 情况 | HTTP / code | 行为 |
|---|---|---|
| 成功 | 200 / 200 | nil 数据为 null，可用 WithMessage 自定义文案 |
| validation.Errors | 400 / 400 | 仅首条安全消息，data 为 null；业务 `%w` 包装后同样识别，底层保留完整 Issues，见 [字段校验](../validation/README.md) |
| httperror.Error | Status / Code | Status 限制 400–599，否则落 400；Code 默认取 Status |
| 未知错误、未提交前 panic、序列化失败 | 500 / 500 | 隐藏内部原因，日志保留 trace 和错误 |
| 返回前或序列化后 deadline 已过 | 504 / 504 | 不再提交成功包络 |
| 客户端取消 | 不追加写出 | 按 DEBUG 记录一次（默认级别不输出）；不把客户端取消当成可送达的错误响应 |
| 请求仍有效，但业务操作返回 context.Canceled | 500 / 500，或显式 httperror.Error | 按普通错误处理，避免空的 200 响应 |
| 响应提交后 panic | 保留已提交状态，中止传输 | 记录原因、aborted=true；不追加错误 JSON |

业务错误示例：`&httperror.Error{Status: 409, Code: 1001, Message: "订单状态冲突", Err: cause}`。Message 是公开内容；内部详情放 Err。HTTP 原生中间件可调用 `respond.WriteError(w, r, err)`，必须在响应提交前调用。

类型化业务入口可通过 `binding.Options{MapError: moduleError}` 集中转换模块错误，回调直接返回业务原因。它只处理业务回调错误，保留原始 cause 和取消语义；完整用法见 [分类业务示例](../examples/business/README.md)。

JSON 遵循 `encoding/json`：`omitempty`、`,string`、匿名字段选择、自定义 Marshaler 均保留。`time.Time` 默认 RFC3339；需要毫秒数时显式声明 `respond.MillisTime`：

```go
type Order struct {
    ID        int64               `json:"id,string"`
    CreatedAt respond.MillisTime  `json:"createdAt"`
    PaidAt    *respond.MillisTime `json:"paidAt,omitempty"`
}
// CreatedAt: respond.MillisTime(time.Now())
```

分页普通结果 `pagination.Page` 固定包含五个字段：`current`、`size`、`total`、`pages`、`records`。先以 `pagination.NewWindow` 校验窗口，再调用 `NewPage(records, window, total int64)`；无总数查询使用独立 `NewSlice(records, window)`，多查一条推导 `has_more`，不输出 total/pages。空记录为 `[]`；框架不自动 count。HTTP 层直接使用统一模型。详见 [分页说明](../pagination/README.md)。

## 超时、流式与 HTTP 限制

`router.Options.Timeout` 默认 30s，负数禁用。deadline 是协作取消：服务、数据库和外部调用必须传递 ctx。Adapter 在提交前检查 deadline；原生 `http.Handler` 自己负责响应及 deadline 检查。框架不启动脱离请求的 goroutine，不尝试强制终止业务代码。

SSE/文件下载可直接写 `w`，随后返回 `(nil, nil)`。Flush、Hijack 和最终状态码会阻止再次追加包络。建议通过 `http.NewResponseController(w)` 操作流；它能返回不支持接口的错误。已提交后的错误仅记日志，不能改写 HTTP 状态。劫持连接由其所有者自行管理和关闭。

混合普通与流式路由：关闭全局 deadline，在普通 API 组使用 `middleware.Deadline(30*time.Second)`；SSE 组单独管理断开和心跳。

`server.New` 默认 read-header 5s、read 30s、idle 60s、header 1 MiB；write timeout 默认 60s，长连接或 SSE 需通过 `server.NoWriteTimeout` 显式禁用。`NewWithOptions` 可调整这些限制。JSON body 上限由 DecodeJSON 执行，其他上传 handler 自己设置上限。框架不盲信 X-Forwarded-For，可信代理策略由部署侧明确配置。

`server.Options` 还提供 `TLSConfig`、`ErrorLog`、`BaseContext`、`ConnContext` 和 `ConnState`。配置 TLS 后 Run／Serve 都启用 TLS；证书加载与更新由应用管理，详见 [server](../http/server/README.md)。

## 生命周期与健康检查

`app.Hook{Name, Start, Stop}` 按注册顺序启动，逆序退出。Start 失败时，失败 hook 自身和已经启动的 hooks 都执行 Stop，因此 Stop 必须容忍部分初始化。启动默认总预算 30s；退出默认总预算 15s。示例通过配置覆盖退出预算为 10s。

Start 的 ctx 只用于初始化操作，启动完成后会取消；后台任务应使用单独管理的生命周期 context。Hook 必须响应 ctx，框架无法强制终止阻塞的用户回调。Stop 接收共享退出预算，即使预算已耗尽也会被调用，以便执行不阻塞的最后清理。

`app.Options.CleanupReserve` 可在总退出预算内预留资源清理时间，摘流及排空使用更早的截止时间；底层 API 默认 0，配置默认 2s。它不会延长总退出时间，也不能中断不响应取消的回调。

退出顺序为 `OnShutdown → 可选 ShutdownDelay → Service.Shutdown → 必要时 Close → 逆序 Stop`。ShutdownDelay 包含在总退出预算内，等待期间继续处理残余流量；服务自身退出时跳过或中断等待。正常排空会等待在途 HTTP 请求。强制关闭只会关闭网络连接并触发请求取消，业务代码仍必须响应取消；升级连接和后台任务应注册自己的 Hook。Run 只能调用一次。库不调用 os.Exit，不接管进程信号。

`health.New(health.Options{Checks: ...})` 产生 Checker，传给 router.Options.Health。`SetReady(false)` 用于退出前摘流；`/healthz` 继续表示进程存活，`/readyz` 返回 503。默认总检查预算 1s，必需探测共享 4 个槽位，可选探测独立共享 1 个槽位。`Check.Optional=true` 的探测失败返回 200／degraded，必需探测失败仍为 503；卡住的可选探测不会占用必需槽位。Probe 必须支持取消。该分类只影响运行期就绪，不改变资源启动失败回滚策略，详见 [health](../health/README.md)。

多个关键服务和后台任务可通过 [`app.NewGroup`、`app.NewTask`](../app/README.md) 统一交给 App；任何成员提前退出都会触发整体退出处理。Group 并发停止成员，不推断成员间的依赖顺序，业务须自行处理需要有序排空的依赖。

批量调用可用 [`concurrency.Map`](../concurrency/README.md) 控制 worker 数并保持输出顺序；单次操作准入用 `Limiter.Acquire` 或不等待的 `TryAcquire`。`NewBoundedLimiter(capacity, maxWaiters)` 同时限制执行与等待数量，等待位满返回 `ErrOverloaded`，等待时间由 context 控制；原有 `NewLimiter` 只限制执行数量。HTTP 可组合 [`middleware.Admission`](../http/middleware/README.md#admission)，服务过载或准入等待超时返回 503。这些能力不提供分布式配额或自动重试。

## 基础设施与配置

`mysql.Open(mysql.Options{DSN: ...})` 或 `postgres.Open(postgres.Options{DSN: ...})` 返回同一种 `*database.DB` 惰性连接池；默认 max-open 25、max-idle 5、idle-time 5m、lifetime 30m。业务只需导入实际使用的适配包；自定义驱动可用 `database.Open(connector, poolOptions)`。可经 `SQLDB()` 接入 Ent/sqlc。DSN 格式与 SQL 方言仍由驱动决定，MySQL 占位符为 `?`，PostgreSQL 为 `$1`。池大小应结合实例数量和数据库连接预算设置，见 [数据库说明](../database/README.md)。

`db.WithTx(ctx, txOptions, func(tx *sql.Tx) error { ... })` 成功提交、错误回滚；panic 或 goroutine 提前退出也会执行回滚清理。回调不得自行提交或回滚。基础设施 Close 幂等且返回错误，交给 Hook.Stop 统一处理。

`redis.Open(redis.Options{Addr: ...})` 支持 Username、Password、DB、TLSConfig、PoolSize 和读写超时；默认最大活跃连接 10，dial 5s、read/write 3s、pool-wait 4s，启用 context 超时。可通过 Client 调用原生 go-redis API。

`Open` 保持惰性并拒绝非零 MinIdleConns。需要预热时，在资源启动回调内使用 `redis.OpenReady(ctx, opts)`；失败自动关闭客户端，成功后仍由调用方关闭。命令 deadline 与驱动后台建连的取消边界见 [Redis 说明](../redis/README.md)。

命令重试默认关闭。`Options.MaxRetries` 为非负重试次数，正值显式开启；示例通过 `REDIS_MAX_RETRIES` 配置。服务端可能已执行命令但响应丢失，此时重试 INCR 等非幂等操作会重复执行。关闭重试仍可能得到“执行结果未知”的错误，业务需要按操作语义设计幂等或查询确认，不能将错误直接视为未执行。

## 可复用资源装配

[`app/resource`](../app/resource/README.md) 将创建、启动探活、清理与健康检查连接起来，减少每个服务重复编写 Hook。构造适配器不访问外部服务：

```go
cache, err := resource.New(resource.Options[*redis.Pool]{
    Name: "redis",
    Open: func(context.Context) (*redis.Pool, error) {
        return redis.Open(redis.Options{Addr: "localhost:6379"})
    },
    Probe: func(ctx context.Context, c *redis.Pool) error { return c.Ping(ctx) },
    Close: func(_ context.Context, c *redis.Pool) error { return c.Close() },
})
if err != nil { return err }
// 注册到 app.Options.Hooks 与 health.Options.Checks：
hook := cache.Hook()
check := health.Check{Name: cache.Name(), Probe: cache.Probe}
```

应用启动成功后可通过 `cache.Value()` 获取资源；更完整的用法见包文档和 basic 示例。基础设施仍可独立使用，不强制经过装配适配器。

资源不必实现固定接口：例如 SDK 的 `Shutdown(ctx)` 可直接适配到 Close。Probe 可省略，此时只检查生命周期状态。`app/resource` 不依赖 `health`，是否注册就绪检查由应用决定。

## 出站 HTTP 和 JSON 方法

[`http/client`](../http/client/README.md) 提供独立连接池及有限超时；一个 client 应复用于同类上游调用。

```go
client, err := httpclient.New(httpclient.Options{})
if err != nil { return err }
defer client.CloseIdleConnections()
var result struct { Name string `json:"name"` }
err = httpclient.DoJSON(ctx, client, http.MethodGet, endpoint, nil, &result)
```

`DoJSON` 默认限制响应读取为 1 MiB，并负责关闭响应体；非 2xx 返回 `*httpclient.StatusError`。需要手工处理响应时用 `Do` 并自行关闭 body。默认不跟随重定向，不增加应用级自动重试；标准 Transport 对可重放请求的处理不代表 exactly-once。出站错误仍由业务 handler 映射为公开的 `httperror.Error`。

带认证头、自定义头或签名的请求可先构造 `*http.Request`，再用 `httpclient.DoJSONRequest(client, req, &result)`，复用同样的响应大小限制、解码和关闭逻辑。

## 可选标准观测

[`telemetry/otel`](../telemetry/otel/README.md) 使用显式 provider，不安装全局 provider 或 exporter。入站用 `adapter.Handler("service-name", router)` 包裹整个 router，出站用 `httpclient.Options{WrapTransport: adapter.Transport}`；日志使用 `telemetry.LogHandler`，从 context 添加 `otel_trace_id`／`otel_span_id`，保留原有关联 ID。

`MeterProvider` 未提供时为 no-op；默认只传播 W3C TraceContext，不自动传播 baggage。SDK/exporter 的创建、采样、flush 和 shutdown 由应用在生命周期中装配。此包提供接入机制，不包含 Collector、仪表盘或告警部署。

chi 路由在 `router.Options.Middleware` 中安装 `telemetry.Route`，将匹配模板传入 span 和指标的 `http.route`；外层仍由 `adapter.Handler` 包装。不要将原始 URL 或业务 ID 作为指标标签。

| 环境变量 | 默认 | 约束 |
|---|---|---|
| APP_ENV | dev | dev/test/prod |
| APP_NAME / APP_VERSION | basic / dev | 日志服务名与版本 |
| INSTANCE_ID | 进程级随机 ID | 显式值必须副本间唯一，字母/数字开头，只含字母/数字/下划线/连字符，最长 128；排除设备保留名 |
| HTTP_PORT | 3000 | 1–65535 |
| HTTP_TIMEOUT | 30s | 正时长 |
| SHUTDOWN_TIMEOUT | 10s | 正时长 |
| STARTUP_TIMEOUT | 30s | 资源启动总预算 |
| SHUTDOWN_CLEANUP_RESERVE | 2s | 总退出预算内的清理预留；与摘流等待之和小于总预算 |
| SHUTDOWN_DELAY | 0s | 非负，小于 SHUTDOWN_TIMEOUT；部署示例使用 5s/30s |
| LOG_LEVEL | info | debug/info/warn/error |
| LOG_OUTPUT / LOG_DIR | stdout / logs | stdout/file/both；文件放入实例子目录 |
| LOG_FORMAT | console | console 为可读文本；json 为结构化输出；sls 对齐 message/env/level/pid |
| LOG_MAX_SIZE_MB | 100 | 正整数，单位 MiB |
| LOG_MAX_BACKUPS / LOG_MAX_AGE_DAYS | 10 / 7 | 非负；0 关闭相应限制，两项共同生效 |
| LOG_COMPRESS / LOG_TIMEZONE | true / UTC | gzip 开关；日志时间与归档名共用 IANA 时区 |
| DATABASE_URL | 空 | basic 按需注册数据库；records 必填；configured 不装配数据库 |
| DATABASE_DRIVER | postgres | 推荐 PostgreSQL；业务选择驱动，配置层无白名单，适配器必须匹配 DATABASE_URL |
| ID_NODE | 0 | 雪花 ID 节点号 0–1023；多实例须分配不同节点，请求关联 ID 与业务 ID 共用生成器 |
| DB_MAX_OPEN_CONNS | 25 | 正整数 |
| DB_MAX_IDLE_CONNS | 5 | 正整数，不超过 max-open |
| REDIS_ADDR | 空 | 未设置时示例不注册 Redis |
| REDIS_PASSWORD | 空 | 不输出到日志 |
| REDIS_MAX_RETRIES | 0 | 命令重试次数；0 关闭，正值显式开启 |
| REDIS_USERNAME / REDIS_DB | 空 / 0 | ACL 用户、数据库编号 |
| REDIS_POOL_SIZE | 10 | 连接池上限 |

`appconfig.LoadFrom(lookup)` 可独立测试，不修改进程环境。业务配置在消费方自行组合，prod 服务可以没有数据库。

配置按 `Service`、`HTTP`、`Lifecycle`、`Database`、`Redis`、`Log`、`ID` 分组。代码构造时先用 `cfg := appconfig.Defaults()`，修改例如 `cfg.HTTP.Port`、`cfg.Log.Compress` 后调用 `cfg.Validate()`；校验不会重置显式的 0/false。环境变量名保持上表约定，详见 [服务配置](../app/config/README.md)。完整预设会校验全部组件；业务也可只组合实际使用的组件。

## 日志、指标与追踪边界

生产接线使用 `logger.Open(RuntimeOptions)`，初始化返回错误，业务使用 `Runtime.Logger()`。文件会在启动时实际打开；运行时维护主动轮转、动态级别、关闭及写入失败计数/回调。日志最后关闭，关闭后不会重新打开文件。stdout 和文件分别尝试写入。运行期压缩和清理由单个后台 worker 处理，队列容量为 16；队列满时轮转背压，Close 等待维护完成。启动保留清理仍同步执行。慢 I/O 和压缩积压会影响写入与关闭耗时；保留策略不是硬磁盘配额，也不承诺 fsync 持久性。完整 API 见 [logger 文档](../telemetry/logger/README.md)。

`cfg.Service.Metadata()` 与 `startup.LoggerOptions(startup.LogSettings{Service: cfg.Service, Log: cfg.Log})`（导入 `app/startup`）将统一配置接入实例属性和独立文件路径。示例不会修改全局 slog logger。Frame 作为库被业务工程导入，镜像和部署清单由业务仓库维护；框架相关约束见 [业务服务运行约束](operations.md)。

数据库连接寿命、Redis 各阶段超时和验证 TLS／mTLS 参数详见 [服务配置](../app/config/README.md)。SQL 池选项由业务显式组装，Redis TLS 映射位于 `app/config/redis`，读取配置本身不加载具体驱动或证书文件。

每次请求完成输出 method、实际 path、路由模板 route、status、bytes、duration_ms、trace_id、canceled、aborted。path 保留 URL 编码，不含 query，超过 2048 字节时使用 `<path>`。访问日志不采集请求参数；操作日志由接口的 `Operation.Request` 分别声明 query / params / JSON body 的 Enabled/Disabled/Inherit，Recorder 默认采集全部字段；服务模板显式关闭参数采集，由接口开启并声明白名单；保留脱敏和大小限制，见 [操作日志](../telemetry/operation/README.md)。认证头不采集。取消或协议中止且尚未提交响应时，status 为 0，避免把中止计为成功。QuietHealth 可抑制健康请求的成功日志。OnRequest 是同步、并发安全的指标接入点，回调应快速返回；指标标签使用 `RequestEvent.Route` 和 `RequestEvent.MetricMethod`，后者将非标准方法归为 `_OTHER`。原始 `Method` 与实际 `Path` 留给日志，不用作指标标签。

默认 trace ID 为进程共享的节点 0 雪花 ID，以十进制字符串输出。服务模板按 `ID_NODE` 创建生成器，多实例须分配不同节点号；已有业务 ID 生成器通过 `WithSnowflake` 共用。上游 ID 只接受最长 128 个 ASCII 字母、数字及 `._-`。生成失败报告观察者并返回通用 500，不切换算法。这里的 traceId 是请求关联标识，**不是 W3C traceparent 或 OpenTelemetry Span**；分布式追踪中间件通过 router.Options.Middleware 接入。

StackOnError 默认关闭。它记录日志发生位置的堆栈，不是错误创建位置的堆栈；常规访问日志不宜开启全量堆栈。

请求 panic 的原因和栈由 Recoverer 记录一次，错误响应不重复记录同一原因。访问日志仍记录请求结果；后续响应写出失败属于独立故障，仍会记录。

后台 Task／Group 的 Run panic 则保留发生位置：用 `errors.As(err, &panicErr)` 提取 `*app.PanicError`，显式读取 `Stack()`；普通错误文本不会自动附带堆栈。
