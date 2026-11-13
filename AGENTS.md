# 仓库协作约定

先读[设计说明](docs/design.md)、[整体架构](docs/architecture.md)和[文档导航](docs/README.md)。

## 变更与文档流程

- GitHub 默认分支为 `main`。日常改动的 PR 目标为 `dev`，准备发版时再将 `dev` 合入 `main`。工作分支按 `类型/主题` 命名，例如 `fix/review-remediation`。
- 本地工作分支不推送到远端。推送前核对目标仓库和分支，显式指定要推送的分支。
- 提交、推送和发布按用户授权分别执行。推送时使用明确的源分支和目标引用，避免批量推送分支或标签。
- 数据库和 Redis 凭据、API 令牌及签名私钥通过本地环境或部署平台的密钥配置注入。暂存前检查配置文件，仓库只保存示例值。
- 提交信息用英文 Conventional Commits:`type(scope): 祈使句描述`;scope 取包或能力名(`fix(app):`、`feat(trace):`、`perf(httpclient):`),跨包变更可省略 scope(`refactor:`、`chore:`、`docs:`)
- 文档、README 用中文撰写;提交信息、代码标识符用英文;代码注释两种语言并存,跟随所在文件现状即可
- 新增或变更公开能力时同步更新,缺一视为未完成:
  - 对应包 `README.md`(能力与边界)
  - `docs/packages.md` 能力目录(新包必须登记,含导入别名;包名与路径末段不同时特别标注)
  - `docs/architecture.md` 两处:"公共方法放在哪里"表登记新公开入口;包边界表登记职责与依赖方向;能力范围变化时同步 `docs/README.md` 的交付与验证边界声明
  - `docs/reference.md` 的能力与边界表，能力或适用范围变化时更新
  - `examples/` 至少一处真实调用——公共 API 没有示例调用视为可疑设计
- 设计取舍维护在 `docs/design.md`，实现契约维护在 `docs/architecture.md`。不新增过程设计、计划或评审档案。实施顺序与真实验证结果在对话中说明。
- 提交前验证按 本文的测试与验证约定 执行;提交粒度按能力划分,一个提交只做一类变更(fix/feat/refactor/docs 不混提)

## 发布

- 以根目录 `VERSION` 为版本声明，实际可下载版本以 GitHub 标签及远程消费结果为准。
- 按 [版本发布](docs/releases.md) 核对版本声明、标签归属和远程消费。GitHub 标签确认前，本地联调使用服务模板文档中的工作区方式。

## 库边界与依赖

frame 是被业务服务导入的库,公共 API 变更即破坏使用方。

- 新增第三方依赖前必须回答:标准库为什么不够、哪个消费场景在复用、能否做成可选适配包隔离;核心包不允许新增非标准库传递依赖——`internal/architecture/dependencies_test.go` 会用 `go list -deps` 拦截,合理例外需在该测试中写明理由
- 不引入:全局单例、`init()` 自动装配、服务定位器、反射 DI 容器;对象由业务入口显式构造、注入、关闭
- 不建 `utils`/`common`/`helpers` 杂物包;基础能力使用职责明确的独立包名,例如 id、pagination、validation
- `internal/` 是 Go 导入可见性边界;开发及消费验收工具隔离在 `internal/dev`、`internal/consumer`,不进公开运行能力的依赖链
- 资源所有权:谁创建谁关闭;`Close` 幂等且返回 error,交给 `Hook.Stop` 统一处理;`Start` 失败时已启动的 hook 都要执行 `Stop`,所以 `Stop` 必须容忍部分初始化
- 不掩盖责任:禁止吞错打印、万能 recover、忽略 ctx 的 goroutine、泛型自动重试;错误显式返回调用方,由调用方决定公开文案
- 框架不做:登录业务、权限、租户、ORM 绑定、Dockerfile/部署清单、注册中心、自动降级缓存;这些由业务仓库决定
- 改公共 API 签名前,先查 `examples/` 与 `templates/` 的全部调用点——它们同时是库用法的编译期验证

## 公共 API 约定

适用于所有导出符号。完整词汇表见 `docs/naming.md`,能力入口归属见 `docs/architecture.md`。

- 构造词只用固定词汇:
  - `New` 构造包的主要对象,不启动后台服务;包内多种构造结果用 `New<Type>`(如 `NewWindow/NewPage/NewSlice`)
  - `Open` 打开需释放的资源;不隐含探活成功,网络可达性用显式 `Ping`/`Probe` 验证
  - `Load` 从外部来源读取成值;`Prepare` 预检结构并声明未执行的步骤;`Parse` 解析单一格式;`Decode` 绑定到目标类型;`Validate` 检查已有值,不修数据、不启动资源
- 生命周期:`Run`(阻塞执行,返回即结束或失败)/ `Start`(启动阶段)/ `Shutdown`(有预算的优雅停止)/ `Close`(释放所有者持有的资源)。不合并三者语义;预算、等待、幂等差异分别写进文档
- 禁止:`Init`/`Do`/`Handle` 等说不清操作含义的通用入口;用 `Async` 后缀替代真实的启动/等待/退出协议;只读访问器加 `Get` 前缀(直接 `Size()`、`Offset()`);`I` 接口前缀;`Impl`/`Base` 后缀;`Manager`(仅限确实管理版本和更新的对象,如 `reload.Manager`)
- 类型命名:配置数据用 `Config`/`Settings`,构造策略用 `Options`,边界策略用 `Limits`;缩写统一 `ID`/`HTTP`/`URL`/`SQL`/`JSON`/`API`/`DSN`,不写 `RequestId`
- API 收敛期发现命名或语义不清:直接改名,并同步调用方、测试、文档、示例;不留别名、Deprecated 转发或第二套实现
- 根包只保留 doc.go 模块说明;公开 API 由能力包拥有,业务入口显式注入消费者需要的依赖,不增加根门面或资源查询容器
- 字段映射显式声明:JSON/配置路径/SQL 列用 `snake_case` tag,不自动推导;对外毫秒时间用 `respond.MillisTime`,裸数时长字段必须带单位名(`timeout_ms`);分页固定五字段 `current/size/total/pages/records`,无总数用 Slice(`current/size/has_more/records`),不伪造 total
- 错误:哨兵用 `ErrXxx`,结构化错误类型用 `XxxError`(如 `FieldError`/`PanicError`);判断一律 `errors.Is/As`,不匹配错误文本;规则码用固定常量,不用用户输入当错误码或回显进消息

## Go 编码规范

- gofmt 是唯一格式真理,不讨论风格
- error 显式处理,禁止 `_ = err`;禁止 panic 出库
- ctx 永远第一个参数
- 并发安全靠 `-race`(需 cgo;Windows 无 gcc 时本机不可用,依赖 Linux CI)
- 需要空类型用 any,不写 interface{}
- feature 内聚:模块间不互调 repo,跨模块协作走 service 层显式依赖
- 测试与源码同目录(`xxx_test.go`);需要黑盒时用 `package xxx_test`

## HTTP 层约定

- handler 一律包 `respond.Adapter` 返回 `(data, error)`,不在 handler 里直接写响应;SSE/文件下载直接写 `w` 后返回 `(nil, nil)`,流操作建议经 `http.NewResponseController(w)`
- 原生 `http.Handler`/中间件中的业务错误用 `respond.WriteError(w, r, err)`,必须在响应提交前调用
- 错误映射固定,不自行发明状态码方案:
  - `validation.FieldError` 返回 400、通用消息和 `data: null`。 `validation.Errors` 返回 400、首条安全消息和 `data: null`
  - `httperror.Error` → Status(限 400–599，越界返回 500)/Code;Message 是公开文案,内部原因放 `Err`
  - 未知错误、序列化失败 → 500,隐藏内部原因,日志保留 trace 与错误
  - 客户端取消不追加写出;请求仍有效但业务返回 `context.Canceled` 按普通 500 处理,不返回空 200
- 输入解析用 `request.DecodeJSON/DecodeQuery/DecodePath`:JSON 默认限 1 MiB;未知字段、空/null 请求体、多段 JSON 映射 400,超大小限制映射 413,错误 Content-Type 映射 415;覆盖大小用 `DecodeOptions{MaxBytes}`
- 日志与错误不携带敏感信息:访问日志不记录 query/body/认证头;验证错误不含完整输入或密钥;httpclient 错误文本不含远端响应体与 URL 查询凭据;不盲信 X-Forwarded-For,可信代理策略由部署侧配置
- 指标标签只用 `RequestEvent.Route`(路由模板)与 `MetricMethod`(非标准方法归 `_OTHER`);禁止用 trace_id、用户 ID、完整 URL、原始自定义方法当标签
- trace ID 是请求关联标识,不是 W3C traceparent,也不是可信身份;上游 ID 用 `trace.ValidID` 校验(≤128 个 ASCII 字母数字与 `._-`);分布式追踪经 `router.Options.Middleware` 接入 otel,由 `adapter.Handler` 包裹整个 router
- 超时是协作取消:新增的服务调用、数据库查询、外部请求必须传递 ctx;混合流式与普通路由时关闭全局 Timeout,普通 API 组用 `middleware.Deadline`,SSE 组自行管理断开与心跳

## 测试与验证

日常变更在本地执行验证。版本标签触发 GitHub Release 工作流并执行发布验证。

- 提交前 `go test ./... -count=1` 必须通过,或直接跑 `./scripts/verify.ps1` / `sh scripts/verify.sh`(格式+测试+vet+构建);gofmt 是唯一格式真理
- 测试与源码同目录(`xxx_test.go`),黑盒用 `package xxx_test`;断言用 testify(`require`/`assert`),并发时序类测试可用纯标准库 `testing` + 信号 channel(参考 `concurrency`)
- 集成测试显式开启,未设环境变量时输出 SKIP——必须确认输出包含实际执行,不能把 SKIP 当验证通过:
  - `FRAME_TEST_MYSQL_DSN` → `go test ./database/mysql -run '^TestIntegration$'`
  - `FRAME_TEST_POSTGRES_DSN` → `./database/postgres`
  - `FRAME_TEST_REDIS_ADDR` → `./redis -run '^TestRedisIntegration$'`
- `templates/service` 是独立 module,根目录测试覆盖不到,用 `./scripts/verify-service.ps1` 验证
- `-race` 需要 cgo + C 工具链;Windows 无 gcc 时本机不可用,依赖 Linux 环境执行,不因此默认配 CI
- 核心包的依赖方向由 `internal/architecture/dependencies_test.go` 强制;新增合理的跨包依赖时在该测试补带理由的例外,不允许删除断言来放行
- 公开行为变更时同步更新对应包的 README.md;`examples/` 必须保持可编译,它们是库用法的编译期验证
