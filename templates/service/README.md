# 独立业务服务起点

本目录用于创建自己的业务工程，包含问候功能、配置、日志、健康检查和资源生命周期。
SQL/Redis 默认关闭，认证按业务需要显式接入。业务按功能聚合，库组件仍从各自能力包导入。

只想理解启动机制，可先读 [basic](../../examples/basic/main.go)；
需要 DTO、VO、Service、Store 和事务的完整参考，读[分类业务](../../examples/business/README.md)。
档案授权、强类型下游调用和设备事件恢复见分类业务目录中的
[profile](../../examples/business/internal/profile/README.md) 与
[devicesync](../../examples/business/internal/devicesync/README.md)；这些示例及客户端错误分类从 v0.2.2 起提供，
与当前模板依赖版本一致。
其他能力接线见[示例导航](../../examples/README.md)。

把本目录复制到新仓库，使用 Go 1.27.1。它是独立模块；框架仓库根目录的
`go test ./...` 不会包含这里。

本目录是工程参考，不提供生成命令；由业务或 AI 根据实际需求复制并调整模块路径。

Dockerfile、编排清单和发布流程由业务工程按实际环境维护；本模板只提供应用代码和开发入口。

请求关联 ID 使用雪花生成器，`ID_NODE` / `framework.id.node` 默认 0；多实例须分配不同节点。
生成器在启动时创建并复用，日志、响应头和响应包络使用同一个十进制字符串 traceId。
模板挂载操作日志记录器，默认关闭 query / params / JSON body 参数采集，仍记录请求结果。
需要留痕的接口显式开启对应来源并选择字段白名单。
自定义脱敏名单与内置、上层名单累加。模板固定 v0.2.6，与当前包布局和启动 API 一致。
字段选择、内置脱敏和大小限制见 [操作日志](../../telemetry/operation/README.md)。

普通 router 使用 With 声明策略；NewAuthenticated 使用 Route.Operation。
例如在功能 routes.go 中为一个入口选择允许采集的正文键：

```go
router.Route{Method: http.MethodPost, Path: "/greetings", Operation: operationhttp.Metadata{
    Request: operationhttp.RequestOptions{Body: operationhttp.Enabled, BodyFields: []string{"name"}},
}}.Bind(New(service))
```

bootstrap 的 composeFeatures 已为功能声明挂载 operationhttp.With；声明未开启的来源继续保持关闭。

接入认证时，保留 feature 返回的 Route 声明和现有 Service/Handler，在 bootstrap
集中汇总声明并通过 router.NewAuthenticated 的 Routes/Groups 编译，显式注入 Verifier
及公开的健康端点。依赖资源的 feature 在资源就绪的组合 Hook 中构造，所有路由装配
都须在 HTTP 开始监听前完成。后续增加数据库或多个 feature 时，继续由 bootstrap
注入具体依赖，事务范围由用例定义；业务层无需接收整个框架配置或资源集合。

`TRUSTED_PROXIES` / `trusted_proxies` 可配置逗号分隔的代理 IP/CIDR，默认不信任转发头；
HTTP 使用 `request.ClientIP(r)` 取得已解析的地址，此接入属于v0.1.3。

1. 将 `go.mod` 的 `example.com/service` 改为业务仓库模块路径。
2. 同步修改 `cmd/api` 和 `internal` 下所有 Go 文件中的
   `example.com/service/internal/...` 导入路径。
3. 使用模板固定的 v0.2.6，运行 `go mod tidy`。如果 GitHub 仓库为私有，请使用本机 Git 凭据，并将该模块加入 GOPRIVATE。

在复制后的业务目录执行：

```powershell
go get github.com/maxtanforge/go-frame@v0.2.6
go mod tidy
```

将 go.mod 和 go.sum 提交到业务仓库。本地联调可临时替换：

```powershell
go mod edit "-replace=github.com/maxtanforge/go-frame=D:/github/go-frame"
go mod tidy
```

路径替换成自己的框架目录；发布业务前删除本地 replace 并固定所选版本。
工作区验收只在临时副本内添加 replace。远程模式验证 GitHub 上实际可读取的版本。
使用固定版本前先确认对应标签已发布。标签缺失时，可按上面的本地 replace 步骤联调。

## 开发、检查和构建

以下命令在复制后的业务目录执行：

```powershell
go mod tidy
go run ./cmd/api config check --config config.yaml
go run ./cmd/api config explain --config config.yaml
go run ./cmd/api version
go run ./cmd/api run --config config.yaml
go test ./...
go vet ./...
go build -o bin/api.exe ./cmd/api
```

Unix 将输出路径改为 `bin/api`。注入版本信息的构建示例：

```powershell
go build -ldflags '-X main.version=1.0.0 -X main.commit=YOUR_COMMIT -X main.buildTime=2026-09-29T00:00:00Z' -o bin/api.exe ./cmd/api
./bin/api.exe version
```

自动重建使用业务 go.mod 已选择的框架版本（本地 replace 同样生效）：

```powershell
go run github.com/maxtanforge/go-frame/cmd/frame dev --dir . --package ./cmd/api -- run --config config.yaml
```

业务模板没有 `./cmd/frame`。开发 CLI 监视 Go 源码；修改 YAML/dotenv 后应重启，
本模板没有启用配置热重载。Windows 开发重启会终止子进程；验证优雅退出时直接运行服务。

## 配置和业务入口

优先级：默认值 < 显式 YAML < 显式开发 dotenv < 环境变量 < `--set path=value`。
不会自动寻找配置文件。开发可使用 `--env-file .env.local`，最终 `APP_ENV` 必须是
`dev`，生产配置拒绝 dotenv。覆盖示例：

```powershell
go run ./cmd/api config check --config config.yaml --set greeting.prefix=Welcome
```

默认 `LISTEN_HOST=127.0.0.1`、`HTTP_PORT=3000`、`GREETING_PREFIX=Hello`。
需要容器对外监听时显式设置 `LISTEN_HOST=0.0.0.0`。检查、解释、版本命令不打开
监听端口、SQL/Redis 连接或日志文件；解释命令输出脱敏配置及来源，检查不代表连通性测试。

`POST /greetings` 接受 `{"name":"Ada"}`，返回框架 JSON 信封，其中业务数据为
`{"message":"Hello, Ada"}`；空白名称和未知字段返回 400。`GET /healthz` 检查存活，
`GET /readyz` 包含已启用的依赖检查。

- `cmd/api`：进程入口、退出码和构建信息。
- `internal/bootstrap`：命令、配置、信号、日志、资源、HTTP 和应用生命周期的显式组装。
- `internal/greeting`：不依赖 HTTP、数据库或框架的业务规则。
- `internal/greeting/http`：同一功能内的 HTTP 适配子包，包名 greetinghttp；请求 DTO 放在 dto_request.go，响应视图对象 GreetingVO 放在 vo_response.go；handler.go 的命名 Handler.Greet 调用业务并显式映射 VO，errors.go 转换公开错误，routes.go 返回整批声明，由 bootstrap 注册。

新增功能按 `internal/<功能>` 聚合业务模型、用例和接口；协议放在该功能的 `http` 子包，
需要持久化时再增加具体 store 适配。适配包向业务包依赖，由 bootstrap 注入资源并装配。
业务无需复制框架的能力包目录，也无需预建 domain/application/adapter 空层级。

需要业务 CRUD 时按 [可选 Ent 接入](docs/ent-crud.md) 装配生成客户端、审计、事务和分页；该扩展要求锁定包含 CRUD 辅助函数的提交。默认问候服务无需 Ent。

模板只校验实际启用的数据库和 Redis 配置；通用 config 解码不决定启用策略。
数据库和 Redis 默认关闭。SQL 推荐 PostgreSQL，默认 `DATABASE_DRIVER=postgres`；设置对应
驱动的 `DATABASE_URL` DSN。Redis 使用 `REDIS_ADDR`、可选的 `REDIS_USERNAME`、
`REDIS_PASSWORD`、`REDIS_DB`；框架也支持 `REDIS_TLS_ENABLED` 和证书配置。
凭据放入部署环境或被忽略的本地文件，不写入样例 YAML。启用依赖后，启动探测及就绪检查
成为必需；HTTP 排空后逆序关闭资源，启动失败会回滚。问候业务本身不访问这些可选依赖。
运行错误由日志生命周期记录，入口设置退出码；检查及命令错误写入 stderr。

## 显式注入业务依赖

`bootstrap.resources` 保留数据库和 Redis 的具体类型资源句柄。构造阶段不打开连接；
资源启动与探测成功后，最后一个 `compose` 启动钩子调用 `resolve`，取得
`dependencies.Database`（`*database.DB`）和 `dependencies.Cache`（`*redis.Pool`）。
未配置的依赖为 nil；配置了却尚未启动或已经关闭的依赖会返回错误。
随后 `composeFeatures` 创建业务服务，调用功能 HTTP 包的 `Routes(service)`，由它使用 `Route.Bind` / `router.Collect` 先收集整批有类型入口，再注册路由；构造失败携带模块、方法、路径和原始原因，所有钩子完成后 HTTP 服务才开始监听。普通 router 不执行 `Public` 认证策略，操作元数据由 bootstrap 显式接入中间件。认证服务应汇总声明后交给 `NewAuthenticated`。

`config check` 验证配置加载和静态约束。模板的 bootstrap 测试另外覆盖离线路由装配和资源回滚，可通过框架仓库的 `scripts/verify-service.ps1` 执行。真实连接、探活和端口监听在目标环境启动时验证。

`runApplication` 先创建空 `app.Group`；组合 Hook 内构造完整 HTTP 路由和服务，再通过
`Group.Add` 注册。新增关键后台任务沿用该入口，无需额外 lazyGroup。正常停机先排空
服务；组合 Hook 的 Stop 再调用 Group.Close，并回收组合期资源，最后关闭 SQL/Redis。
启动部分失败也走同一路径，尚未接收请求的已注册服务同样会被关闭。

需要在组合期打开客户端、provider 或流资源时，命名回调中的 `cleanup *app.Cleanup`
参数，取得资源后立即 `cleanup.Add("具体名称", closeCallback)`；失败时调用者仍须自行
释放未被接纳的资源。Cleanup 逐项逆序关闭、汇总错误和 panic，不让某个失败中断后续
回收；业务服务仍只接收具体依赖，不接收 Cleanup 或整个 dependencies。

greeting 的 HTTP 适配通过 `binding.Options{MapError: httpError}` 配置业务错误转换，回调只返回用例错误。mapper 不处理解码和字段校验错误，也不能吞掉原始原因或改变取消语义。
组装错误与资源启动错误一样触发逆序回滚，不会开始接收请求。

新增需要数据库的业务时，在 `composeFeatures` 中命名依赖参数 `deps`，先检查
`deps.Database != nil`，再显式调用例如 `orders.NewService(deps.Database)`；缓存同理
通过 `deps.Cache` 传给确实需要它的构造函数。依赖缺失或构造失败时返回错误。
构造函数也可接收由具体数据库适配器实现的业务接口，避免领域包导入框架。
不要把整个资源集合传入业务服务，也不要让业务服务自行关闭这些借用的连接。
问候业务只接收前缀，因此示例不会为它虚构 SQL 或 Redis 操作。

业务逻辑放在特性包，HTTP 解码和错误映射放在相应的 `*http` 包；bootstrap 只负责
配置、依赖构造、生命周期和路由组装。新增资源时同步加入 typed 字段、hooks、checks
与 resolve，保持依赖和启动顺序在代码中可见。

## 在框架仓库中验证模板

运行 `./scripts/verify-service.ps1`。脚本将模块复制到唯一临时目录，只在该副本添加
指向框架工作区的验证专用 `replace`，然后执行 tidy、测试、vet 和编译，最后恢复环境
并仅删除自己创建的目录。模块和工具链下载被禁用，依赖必须已缓存，本机必须已安装所需 Go 版本。
仅临时设置并恢复当前进程环境变量，不修改 Go 系统配置；模板源文件保持不变。
测试使用 HTTP recorder 和不可连接的示例依赖地址，不要求外部服务、凭据或开放监听端口。

## 可选认证和操作日志

模板默认不启用认证。启用时先选择包含以下 API 的框架提交，在业务 bootstrap 中显式组装：

1. 在业务配置中定义认证设置，密钥字段标记 `secret:"true"`。结构检查不打开资源；
   运行入口加载受管理的验证公钥和 32 字节加密密钥，调用 `token.NewVerifier`
   （`github.com/maxtanforge/go-frame/auth/token`）。验证服务只持有公钥，不接收签名私钥。
2. 实现并注入 `operation.Sink`（`github.com/maxtanforge/go-frame/telemetry/operation`），
   用 `operationhttp.New`（`github.com/maxtanforge/go-frame/telemetry/operation/http`）创建记录器。
   Sink 的存储、容量与失败处理由业务决定，不在框架中自动写入数据库。
3. 用 `router.NewAuthenticated` 替换普通路由构造，向 `AuthenticatedOptions` 传入
   `Verifier`、`Operations`、`PublicEndpoints` 和 `Routes`。在 `PublicEndpoints` 显式列出
   GET/HEAD `/healthz`、`/readyz`；登录等业务公开路由设置 `Public: true`。
   其余路由默认要求认证，指标端点也需明确决定访问策略。
   将 Timeout、Health、Trace、客户端 IP 解析等共同设置传入 `AuthenticatedOptions.Router`。
   NewAuthenticated 已含完整基础中间件，不再放进 router.New；操作日志通过 Operations
   装配一次。需要路径前缀时才使用不含框架中间件的 chi 父路由。
4. HTTP 用例从 `principal.Require(r.Context())` 取得认证身份；数据写入通过
   `auditprincipal.FromContext`（`github.com/maxtanforge/go-frame/database/audit/principal`）取得审计操作者，
   显式传入事务内的业务用例。为路由设置 `Operation` 元数据，写入成功后按需调用
   `operationhttp.SetResource`，不要从客户端提交的操作者字段推导身份。

账号校验、登录签发、会话禁用检查和权限规则由业务实现。仅在账号凭据校验成功后签发令牌；
对外到期时间通过 `ExpiresAt.UnixMilli()` 输出毫秒。多副本必须使用一致且受管理的
issuer、audience、key ID 和密钥集合，密钥与令牌不能提交到仓库。

v0.1.3 提供资源指标：设置 `METRICS_ENABLED=true` 或 `metrics_enabled: true` 后，模板挂载 `/metrics`，输出 Go 运行时、进程 CPU/RSS 和已配置的 SQL/Redis 连接池统计。默认关闭；启用后由监听网络/网关限制抓取访问，模板不额外添加身份策略。此端点当前输出资源指标，HTTP 延迟指标装配见 observability 示例。

接口 ID DTO 使用 `id.ID`（如 `ID id.ID`），由 binding/Prepared 统一解析 JSON 字符串、query 和 params，无需业务再次规范化或检查整数精度。
