# Go Frame 命名与公共 API 约定

适用于框架、示例及后续新增能力。项目处于基础建设阶段，发现语义不清的入口直接修改并同步
调用方、测试和文档；不为旧名称增加别名、Deprecated 转发或第二套实现。
根包 frame 只保留模块说明；组件各自拥有公开 API，业务通过构造函数显式注入依赖。

## 包与类型

- 导入路径按能力分类，包声明保持小写、简短；路径末段不一定等于 Go 包名，见[导入约定](packages.md#导入路径与包名)。按能力命名：`http/request`、`pagination`、`database/audit`、`app/resource`。
  驱动和适配放在归属能力下：`database/mysql`、`config/loader`。
- 基础能力使用明确的包名，例如 `id`、`optional`、`pagination`、`validation`；避免增加没有独立职责的分类层。
- 不建立 `utils`、`common`、`helpers` 杂物包，不以 controller/service/repository
  将整个框架拆成横向大包。业务内部按 feature 组织，接口由消费方定义。
- 类型用名词，不加 I 接口前缀，不给普通类型加 Impl、Manager、Base 后缀。
  `Manager` 仅用于确实管理版本和更新的对象，如 `config/reload.Manager`。
- 配置数据用 `Config`/`Settings`，构造策略用 `Options`，边界策略用 `Limits`；
  业务请求和响应类型按用途命名，如 `ListQuery`、`CreateInput`。
- Go 标识符使用 `ID`、`HTTP`、`URL`、`SQL`、`JSON`、`API`、`DSN` 等一致缩写，
  例如 `RequestID`，不用 `RequestId`。未导出的首字母缩写如 `httpClient` 保持小写。

## 函数与方法

| 名称 | 约定 | 本仓库示例 |
|---|---|---|
| `New` | 构造包的主要对象；不启动后台服务 | `app.New`、`loader.New` |
| `New<Type>` | 包内存在多个构造结果时，显式说明类型 | `pagination.NewWindow/NewPage/NewSlice` |
| `Open` | 打开或取得需要释放的资源；明确关闭责任 | `logger.Open`、`mysql.Open` |
| `Load` | 从外部来源读取并形成值，可能有文件或环境输入 | `loader.Inputs.Load` |
| `Prepare` | 检查结构并准备后续操作所需状态；明确尚未执行的步骤 | `config.Prepare` 不做业务校验，`Prepared.Decode` 合并后校验 |
| `Parse` | 解析一种具体格式，不装配应用 | `yaml.Parse`、`dotenv.Parse` |
| `Decode` | 将输入转换/绑定到目标类型 | `config.Decode`、`request.DecodeQuery/DecodePath` |
| `Validate` | 检查已有值，不启动资源或修正非法输入 | `Window.Validate`、业务 DTO 的 `Validate` |
| `Run` | 执行阻塞生命周期，返回意味着结束或失败 | `app.App.Run` |
| `Start` | 启动阶段操作，返回后后续阶段可以继续 | `app.Hook.Start` |
| `Shutdown` | 有预算的优雅停止；按所属接口明确 context 和排空语义 | HTTP 服务关闭 |
| `Close` | 释放所有者持有的资源，符合相关接口约定 | `Runtime.Close`、`DB.Close` |
| `Execute` | 分派一个命令或用例，返回执行结果 | `command.Execute` |
| `Snapshot` | 获取一个已发布状态视图，声明复制/共享规则 | `reload.Manager.Snapshot` |

`Open` 不意味着连接探测成功，网络可达性用显式 `Ping`/`Probe` 验证。

`redis.OpenReady` 是显式的“创建并探活”组合操作：成功返回所有权，失败关闭所创建客户端。
`metricotel.NewRequestRecorder` 与 metric 的同名构造体现适配器绑定；其额外 Meter 依赖由调用方提供。
`startup.LoggerOptions` 明确返回日志运行选项；`app/config/redis.Options` 在适配包内映射 Redis 配置并读取所需 TLS 文件。SQL 池选项由业务显式组装。
构造器也不等于绝对纯函数：`loader.New` 会捕获环境输入，具体副作用必须写在文档中。
不把已有接口的 `Shutdown`、`Close`、`Stop` 生硬合并；它们的预算、等待和幂等语义
要分别说明。只读属性直接命名 `Size()`、`Offset()`、`Paths()`，不重复添加 Get 前缀。
业务查询的 `Find`、`List` 等名称随业务语义定义，并明确未找到、空列表和错误的区别。

公开函数中 context 放在首个参数，error 放在最后一个返回值；不保存请求 context
作为长期对象的隐式状态。除必须实现的接口外，不引入 Init/Do/Handle 等无法说明操作
含义的通用入口，也不以 Async 后缀替代真实的启动/等待/退出协议。

调用方必须传入非 nil context。部分构造/生命周期入口提供显式错误诊断，原生操作遵循所用 context/驱动契约；不能依赖所有方法都将 nil 转成 error，也不自动替换为 Background。

## 传输、数据库与时间

声明式请求 DTO 默认按 snake_case 推导名称（UserID → user_id、HTTPCode → http_code）；json 标签覆盖，query/path 可覆盖对应来源。低层 DecodeQuery/Path 仍要求显式来源标签。响应 JSON 使用 encoding/json 原有规则，SQL 名称由 Ent/业务映射，配置命名不随请求规则改变。配置路径和数据库列使用 `snake_case`；与既定外部协议对齐时，以协议的字段名称为准，不能只为风格改名。
普通分页固定 `current`、`size`、`total`、`pages`、`records`；无总数
结果使用独立 `Slice`，只包含 `current`、`size`、`has_more`、`records`。

分页模型描述数据，不能叫 ResponsePage、PageVO 等绑定特定 HTTP 实现的名称。
Page 的 total/pages 必须是数字；Slice 不伪造总数。JSON 空集合输出 `[]`。
雪花 ID 的 Go 内部表示可使用 int64，面向 JavaScript 的传输应使用十进制字符串，
避免超过安全整数范围后失真；records 示例已按此约定接入雪花 ID。

时间点在领域中用 `time.Time`，公开业务时间按既有 `MillisTime` 协议输出毫秒；
字段可命名 `created_at`、`updated_at`，在契约中标明 epoch milliseconds。
时长在 Go 中用 `time.Duration`，裸数形式必须带单位名称，如 `timeout_ms`；
禁止同一个 duration 字段在不同入口分别解释为秒和毫秒。版本构建元数据的日期字符串
需单独声明格式，不与业务时间点混用。

## 错误与诊断

稳定错误哨兵用 `ErrInvalid`、`ErrNotFound` 等；承载结构化信息的错误类型用
`Errors`、`PanicError`。通过 `errors.Is/As` 判断，不匹配错误文本。
规则码应为固定常量，不能使用用户输入作为错误码或直接回显到消息中。
输入错误、开发者 schema 错误和基础设施错误必须区分，不能统一转换成 400。

## 本轮执行与后续检查

本轮统一分页构造器为 `NewWindow`、`NewPage`、`NewSlice`，删除 respond 旧分页入口；
新增请求解码和启动加载器按上述词汇命名。门面、消费示例、SQL 测试和文档同步更新。

新增 API 审查时检查：职责能否由名称推断、是否与已有名称重复、输入来源是否清楚、
资源所有权和时间单位是否明确，以及示例是否真的调用公共 API。
后续分布式锁、批处理、PATCH/并发更新能力实施时沿用同一约定；不提前创建空接口。
已有其他包逐项按行为审查，不宣称本轮完成了全仓所有符号的统一重命名。
