# 开发重启与运行时配置更新

两种更新分别处理：Go 源码变化需要重新构建并切换进程；明确允许的配置字段可以在原进程中更新。业务服务不依赖开发监督器。

```powershell
./scripts/dev.ps1
```

```sh
sh scripts/dev.sh
# 或直接使用统一入口
go run ./cmd/frame dev
```

默认运行 configured 示例，回环监听、启用 YAML 配置更新。业务项目可显式指定：

```sh
go run ./cmd/frame dev --dir /path/to/project --package ./cmd/api -- --config configs/dev.yaml
```

`--` 后为应用参数；工具不会把参数拼成 shell 代码。构建产物在项目 `.frame-dev` 的独立运行目录，不覆盖正在运行的二进制。日志、版本控制、构建输出和依赖目录不参与源码监听；Go 文件及 module 文件变化触发有界轮询/去抖，构建串行执行，构建中再次变化会丢弃过期产物。编译失败保留旧服务；成功后才停止旧进程并切换。新进程启动后异常退出可诊断，但不承诺自动回退、零停机或保留内存状态。

Windows 通过 Job Object 约束构建进程和服务进程树，先把受控助手加入 Job，再允许它启动实际程序，避免子进程在归属设置前逃逸。Windows 重启使用强制停止，不能当作业务优雅退出验证；正常业务关闭行为应另行测试。Unix 使用进程组和有预算的信号退出。服务不应主动脱离监督器的进程树。

停止时还会等待 Job 中活动进程归零或 Unix 进程组消失，超时则阻止新版本启动。Unix 未回收的僵尸进程组可能导致保守超时；强制杀死 Unix 监督程序本身无法执行清理。扫描总目录项、深度和源码总量也有上限，超限保留当前服务并提示错误；具体限制见 [命令说明](../cmd/frame/README.md)。

代码重启与动态配置各有职责：仅调整 YAML 中的日志级别、业务文案、标签不会触发进程重启；端口或数据库等静态变更会提示需要重启。停止再启动开发命令可应用静态配置；不要期待它偷偷替换连接池。

## 工具取舍及验证

实际检查了已安装 Air v1.60.0。无网络测试发现 Windows 上它在编译前停止旧进程，编译失败后无法满足保留旧服务的要求；其[对应版本源码](https://github.com/air-verse/air/blob/v1.60.0/runner/engine.go)也先发停止信号再调用构建。故本仓库没有引入 Air 依赖，也不要求安装或升级全局工具。

`scripts/verify-dev.ps1` 是显式运行的 Windows 进程测试：临时 Go 项目只写心跳文件并启动一个子进程，不监听任何端口。覆盖成功切换、失败保留、文件替换、连续编辑、输出目录排除，以及监督器被直接终止后的子进程清理。测试产物留在临时目录便于诊断；不会修改防火墙或系统环境。普通 `scripts/verify.ps1` 不运行这类较重的进程测试。

使用目标平台执行验证；交叉编译不能替代实际进程验证。

每次发布前，必须在启用 CGO 且具备兼容 C 编译器的目标环境执行完整
`sh scripts/verify.sh --race --consumer`（Windows 对应 `./scripts/verify.ps1 -Race -Consumer`），
保留所验证提交、Go/OS/架构、命令及结果。并发、锁、生命周期或后台任务改动在合并前也应执行。
本机缺少条件时，普通测试只证明非 race 路径；race 项保持未完成，由具备条件的环境补齐后再发布。
日常验证使用本地脚本。版本标签触发 [GitHub Release 工作流](releases.md#发布流程)，复用同一套 Linux 验证命令。


## 独立服务起点

[服务模板](../templates/service/README.md) 提供独立 module、配置和显式 bootstrap 参考。
由业务或 AI 复制并调整模块和导入路径、选择具体框架版本；不提供工程或功能生成器。
开发重启使用 cmd/frame dev，业务的 run/version/config 命令保持独立。

## 独立消费工程验收

在框架仓库运行以下工程工具。它会在自有临时目录复制服务模板为独立业务 module，
并检查模板副本、独立 Ent fixture 和完整分类业务工程的 tidy、测试、vet 与构建，最后清理临时目录。

```powershell
go run ./scripts/consumer --repo . --mode workspace
```

默认工作区模式离线运行，依赖需已缓存；仅临时业务工程使用本地 replace。
不会连接数据库或监听端口，也不会修改系统 Go 配置、Git 和模板源文件。
JSON 验收报告写入 stdout，步骤进度写入 stderr；模式、解析版本和各项状态分别记录。
原始子进程输出不直接透传，避免配置或实库诊断中夹带秘密。
首次运行若报告 `dependency_not_cached`，可显式加 `--network` 获取所选版本的缺失依赖，
再重跑离线模式。Ent fixture 复用所选源码的依赖版本、校验文件及模块图，避免自动选择最新工具依赖。
工作区报告中的 `workspace_dirty` 标识未提交改动；`source_commit` 只是该工作区的 HEAD，
不能把带改动的验收结果当作该提交原始源码的结果。

检查真实进程需要显式开启，仅监听回环地址：

```powershell
go run ./scripts/consumer --repo . --mode workspace --process --budget 10m
```

进程检查验证健康、就绪与请求输入输出。通过业务 bootstrap 的 context 取消进行关闭检查；
这种证据与操作系统信号关闭、Windows 强制终止分别记录，不能相互替代。

远程消费要求具体已推送版本或提交，并显式允许网络下载：

```powershell
go run ./scripts/consumer --repo . --mode remote --version (Get-Content VERSION -Raw).Trim() --network --budget 10m
```

远程模式使用独立模块缓存，不添加 replace，禁用 GOWORK；凭据仍由本机管理。
发布验收使用已推送的版本标签。也可以指定已推送的完整提交 SHA 做预发布验证，Go 会将 SHA 解析为对应版本并记录解析结果。Ent fixture 取自解析后的模块目录，工作区模式取当前源码。
旧提交的验收不代表未提交的新接口已远程可用。

可选 `--race` 要求平台具备 cgo 和 C 编译器，不具备条件不能记为通过。
实库检查通过 `--engine mysql` 或 `--engine postgres` 明确选择，要求相应的
`FRAME_TEST_MYSQL_DSN` 或 `FRAME_TEST_POSTGRES_DSN`；未选择时屏蔽这些实库变量。
工具只运行既有会话临时表测试，不执行持久化业务迁移，也不自动寻找生产连接。
请不要在命令参数中填写 DSN；实库、mock 与未执行项分别记录。
所选远程版本若尚无综合认证测试，会以 `selected_version_test_unavailable` 单独列出，
不借用工作区的新测试冒充该版本的能力。清理失败会保留所有权标记并报告 `retained_temp`，
整体验收仍失败；外部强制终止进程后，工具无法保证执行临时目录清理。

检查消费验收 JSON 中的通过、跳过和平台字段。
业务使用 AI 修改代码时，按[接入与验证指南](ai-coding.md)选择对应版本和示例，
在业务仓库运行自身测试；框架消费验收工具用于框架改动验证，两者不能相互替代。
