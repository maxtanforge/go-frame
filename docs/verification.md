# 本地验证与性能基准

仓库提供本地统一验证脚本。GitHub Release 工作流在推送版本标签时复用这些脚本。脚本不修改防火墙，也不要求全局安装 lint 工具。
在仓库中运行（脚本也支持从其他目录调用）：

```powershell
./scripts/verify.ps1
./scripts/verify.ps1 -Bench -BenchTime 1s
./scripts/verify.ps1 -Race
./scripts/verify.ps1 -Changed
./scripts/verify.ps1 -Coverage -Timing -Consumer
```

```sh
sh scripts/verify.sh
sh scripts/verify.sh --bench --benchtime 1s
sh scripts/verify.sh --race
sh scripts/verify.sh --changed
sh scripts/verify.sh --coverage --timing --consumer
```

脚本先检查受版本管理及未忽略的新 Go 文件是否符合 gofmt，再执行测试、vet、golangci-lint 和构建。
golangci-lint 以钉住版本 `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`
执行：无需本机安装，首次运行经模块代理编译，之后命中构建缓存。linter 集与豁免见 `.golangci.yml`
（含 internal 目录与 Ent 生成目录的带理由豁免；取消同类问题截断，门槛必须看到全部结果）。
格式检查不会自动改文件。任何步骤失败都返回失败状态；请求 race 而缺少 CGO 时会明确失败，
不会静默跳过。race 还需要该平台支持的 C 编译器。运行时遵循当前进程的 Go 环境变量，
例如 GOCACHE；不会执行 `go env -w`。

## 迭代冒烟与测量

`-Changed` / `--changed` 比较当前工作区与 HEAD，包含暂存、未暂存和未忽略的新文件，
测试修改的包及其传递反向依赖（包括测试导入），随后仍执行全模块 vet/build。
重命名同时考虑旧路径和新路径；非 Go 文件变动、删除整个包、独立模板 module 或无法确定范围时，
保守回退为根模块全量测试。它只针对当前平台/构建标签，不检查已经提交的分支差异。
与 `-Race` / `--race` 同用时，依赖图查询也启用 `-race`，纳入 race 条件编译文件中的导入。
没有变动时跳过测试，仍运行 vet/build。冒烟模式跳过 lint，不能代替提交前默认全量验证，
也不能与 Consumer、Bench、Coverage 同用；模板变动还需单独运行 Consumer。

`-Coverage` / `--coverage` 为根模块全量测试写出 `coverage.out`，并打印总体语句覆盖率。
该文件已被 Git 忽略，可用 `go tool cover '-func=coverage.out'` 查看函数覆盖率，
或用 `go tool cover '-html=coverage.out'` 检查未覆盖路径。独立模板不计入该 profile。
比较不同版本时保持 Go、平台、构建标签和测试范围一致；高语句覆盖率不证明所有行为组合正确。

`-Timing` / `--timing` 流式解析 `go test -json`，保留测试输出，汇总最慢的 5 个测试
（包括子测试）及 5 个包，测试失败仍返回失败。时间包含等待和并发调度，父测试包含子测试时间，
不要累加排名项作为总耗时，也不要凭一次结果缩短时序测试的等待预算。

## 发布一致性

在仓库根目录执行：

```powershell
go run ./scripts/release-check
# 已创建本地版本标签后，额外检查 VERSION 对应的标签是否指向 HEAD：
go run ./scripts/release-check --tag
```

入口复用 `TestReleaseVersionConsistency` 检查 VERSION、模板依赖及文档版本声明。
`--tag` 支持轻量和附注标签；缺失或指向其他提交即失败。它只检查本地版本一致性及标签归属，
不创建/推送标签，不证明工作区干净，也不证明远程发布、race 或消费验收已通过。
分支、标签、GitHub Release 和发布后的消费验收见[版本发布](releases.md)。

## 现有外部 CI 接入

如需接入外部 CI，可直接调用统一脚本。以下命令分别适用于 Linux 和 Windows 环境：

```sh
# Linux runner：预装兼容 go.mod 的 Go、Git、C 编译器，启用 CGO。
CGO_ENABLED=1 sh scripts/verify.sh --race --consumer
```

```powershell
# Windows runner：验证平台行为和独立消费工程。
./scripts/verify.ps1 -Consumer
```

`--consumer` / `-Consumer` 在根模块检查后，执行工作区独立服务、Ent 及完整分类业务工程的
tidy/test/vet/build 与综合认证测试。选择 race 时同时将 `--race` 传递到消费验收。
消费验收任一步失败或清理失败都会使统一入口失败；默认不开启进程、实库或远程模式。
根模块测试不会覆盖独立 module，所以正式合并门槛应包含 Consumer。

消费验收默认离线，runner 应先预热框架和独立工程所需依赖，参照
[消费验收说明](development.md#独立消费工程验收)；不要将依赖未缓存的失败视为跳过。
保留脚本完整输出与消费验收 JSON，审核其中 skipped 项。lint 首次执行也需要工具依赖缓存
或模块代理。使用可写的 GOCACHE/GOLANGCI_LINT_CACHE，不修改全局 Go 配置。

正式扩大生产使用前，还需在目标环境完成后端断连恢复、滚动发布摘流与排空、过载拒绝、
大响应内存和日志轮转尾延迟验收。根模块通过、本地 replace 消费通过都不能替代这些检查。
并发准入可通过 `NewBoundedLimiter` 与 `middleware.Admission` 显式装配；在持续超载下采集
执行数、等待数、拒绝数、准入耗时分布及请求 p99，同时检查恢复后占用归零。
其容量边界不代表整个 HTTP 服务的连接数、goroutine 或内存上界。
当前版本为 `v0.2.6`；发布前应完成所选提交的验证、说明变更与支持范围，
发布后再执行不带本地 replace 的远程版本消费验收。本脚本不会自动发布或冻结 API。

`templates/service` 独立 module 的验证提供 PowerShell 与 Unix 两个版本
（`./scripts/verify-service.ps1` / `sh scripts/verify-service.sh`）：复制模板到临时目录、
施加仅验证用的本地 replace、先用默认代理预热依赖图、随后全部离线执行 tidy/test/vet/build。
`verify-dev`（开发热重启的进程级测试）目前仅有 Windows 版；Unix 版依赖真实 Unix 进程语义，
待具备 Linux 环境后再实现，与 `-race` 同等对待。

常规测试只在 loopback 地址启动临时 HTTP/TLS 服务。真实 MySQL/PostgreSQL/Redis 测试仅在显式设置
`FRAME_TEST_MYSQL_DSN` / `FRAME_TEST_POSTGRES_DSN` / `FRAME_TEST_REDIS_ADDR` 时运行；未配置时跳过，不能据此声称真实依赖已验证。

分类业务的 PG 迁移/CRUD 验收单独选择：设置 `FRAME_BUSINESS_TEST_POSTGRES_DSN`，运行
`./scripts/integration.ps1 -Business` 或 `sh scripts/integration.sh --business`。
这个模式只要求该业务 PG 连接，不要求 MySQL/Redis；单连接临时表与实际边界见
[分类服务说明](../examples/business/RUNNING.md)。脚本缺少连接时明确失败，不静默 SKIP。

## 连接恢复与 HTTP 过载

显式提供已指定的测试连接后，执行 `./scripts/resilience.ps1` 或
`sh scripts/resilience.sh`。入口要求 MySQL/PostgreSQL/Redis 三类配置齐全，
恢复失败会返回非零状态；不自动读取其他工程配置。沿用集成测试的 FRAME_TEST_*
变量，Redis 用户、密码与 DB 同样由环境传入，不放进命令行。

恢复测试仅在 `FRAME_TEST_RECOVERY=1` 时启用；直接选择时缺少配置会失败。
SQL 使用真实驱动与共享数据库池，通过测试 DialFunc 把本轮拨号经过 loopback relay，
保留原认证与 SQL TLS 目标。SQL 与 Redis 均验证连接池耗尽的 deadline、切断连接后的失败预算、
恢复转发后同一池/客户端再次 Ping 成功；MySQL 要求 TCP，PostgreSQL 不支持多主机配置。
Redis 入口当前验证明文 TCP 测试连接，不覆盖 TLS。所有检查仅读取/Ping，
不重启远程服务，不执行持久化写入；切断只影响 relay 新建的本轮连接。

`go test ./http/middleware -run '^TestAdmissionSustainedHTTPOverload$' -count=1 -v`
可独立执行回环 HTTP 验收，记录成功/503 数量、执行峰值与准入 p99，并检查停止负载后恢复。
固定客户端与请求数量的结果用于功能回归，不能当作生产吞吐、端到端 p99 或内存上界。
后端进程重启、网络黑洞和真实部署持续负载仍需另行验收。

## 基准测量什么

```sh
go test ./concurrency ./telemetry/logger -run '^$' -bench . -benchmem -benchtime=1s -count=5
```

| 基准 | 每次操作与边界 |
|---|---|
| Map/sequential、workers_1、workers_8 | 处理固定 256 个整数；回调仅做乘法，用于衡量调度和分配开销，不代表网络 I/O 加速比 |
| Limiter/parallel_capacity_* | 并发申请、立即释放一个许可；无业务工作和等待队列长度保证 |
| LogWrite/discard | 并发 JSON slog 编码后写入 io.Discard；预热一次排除时区初始化 |
| LogWrite/file | 相同记录写入临时目录的受管理文件，包含锁竞争、系统调用和偶发轮转；关闭压缩，保留一个归档 |
| LogRotate/compress_* | 轮转固定 64 KiB 文件并等待本轮维护，包含归档、保留清理和可选 gzip；填充文件的时间不计入，最多保留一个归档 |
| LogMaintenance | 空活动文件及无归档目录的维护；不创建归档，单独观察目录扫描和匹配开销 |

轮转输入是高度可压缩的重复文本。生产日志的压缩率、记录大小、文件系统、杀毒软件、
存储延迟和并发量都会影响结果。Go benchmark 的 ns/op 是平均耗时；RunParallel 的 ns/op
是总时间除以完成量，不能视作单次调用延迟，更不是 p95/p99。

这些基准没有 fsync、容器采集器或真实业务负载。跨机器比较时应固定 Go 版本、CPU 配额、
GOMAXPROCS 和文件系统，多次采样。需要评估日志对请求尾延迟的影响时，应在部署环境压测
业务请求并同时观察轮转时段，而不是从平均 ns/op 推算。

## 执行与交叉编译

Linux 上运行脚本才是 Linux 执行验证。在 Windows 上设置 GOOS=linux 后成功 build 或
`go test -c`，只能证明交叉编译通过。Git Bash 执行 Windows Go 也仍属于 Windows 验证。

日志后台归档使用单 worker 和 16 项有界队列，队列满时背压，Close 等待维护完成。
目标环境仍需验证归档错误可见性、退出耗时和重启后的临时文件恢复。

发布版本以根目录 VERSION 为准，workspace 消费验收读取该文件。版本一致性测试检查 README、本文及服务模板依赖；升级时须同步更新这些声明。

解析器 fuzz 在本地显式执行：`go test ./http/request -run '^$' -fuzz '^FuzzJSONPreflight$' -fuzztime=15s -parallel=2`。另两个目标为 FuzzXMLPreflight / FuzzStreamMultipart，分别执行同样命令并替换目标名称。
