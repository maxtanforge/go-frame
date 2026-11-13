# 能力目录与导入约定

模块路径为 `github.com/maxtanforge/go-frame`。只导入实际需要的能力包；根包不提供资源
查询对象。部署模型位于 `app/config`，通用解码位于 `config`，业务入口显式装配依赖。
包路径以 v0.2.0 为基线；当前启动 API 和独立模板采用 v0.2.6，本地联调可显式 replace。

## 按职责选择组件

| 职责 | 组件 |
|---|---|
| 应用运行 | [app](../app/README.md)、[resource](../app/resource/README.md)、[服务命令](../app/command/README.md) |
| 配置与启动 | [通用配置](../config/README.md)、[来源](../config/loader/README.md)、[热重载](../config/reload/README.md)、[部署设置](../app/config/README.md)、[启动日志](../app/startup/README.md) |
| 独立基础能力 | [ID](../id/README.md)、[分页](../pagination/README.md)、[校验](../validation/README.md)、[标签](../validation/tags/README.md)、[部分更新](../optional/README.md)、[并发](../concurrency/README.md) |
| HTTP | [路由](../http/router/README.md)、[请求](../http/request/README.md)、[绑定](../http/binding/README.md)、[响应](../http/respond/README.md)、[健康端点](../http/health/README.md)、server、middleware、client、httperror |
| SQL | [连接与事务](../database/README.md)、mysql、postgres、[Ent](../database/ent/README.md)、[查询](../database/query/README.md)、[审计](../database/audit/README.md) |
| Redis 与缓存 | [Redis](../redis/README.md)、[缓存](../cache/README.md)、cache/memory、cache/redis、[租约](../lock/redis/README.md) |
| 运行支撑 | [健康报告](../health/README.md)、[定时任务](../schedule/README.md)、identity |
| 身份认证 | [主体](../auth/principal/README.md)、[令牌](../auth/token/README.md)、[HTTP 认证](../auth/http/README.md) |
| 观测 | [日志](../telemetry/logger/README.md)、[指标](../telemetry/metric/README.md)、[操作事件](../telemetry/operation/README.md)、[OTel](../telemetry/otel/README.md)、trace |

单 module 共享适配器依赖版本；独立能力不导入不需要的驱动、协议与 SDK。

## 导入路径与包名

多数包名与路径末段相同；以下入口需要留意，含与其他包同名、同时导入需要别名的情况。别名用于避免同时引用时冲突，不创建第二套 API。

| 导入路径后缀 | 声明的包名 | 建议写法 |
|---|---|---|
| `database/ent` | `entdb` | `entdb "github.com/maxtanforge/go-frame/database/ent"` |
| `auth/http` | `auth` | `auth "github.com/maxtanforge/go-frame/auth/http"` |
| `http/client` | `httpclient` | `httpclient "github.com/maxtanforge/go-frame/http/client"` |
| `http/health` | `healthhttp` | `healthhttp "github.com/maxtanforge/go-frame/http/health"` |
| `telemetry/operation/http` | `operationhttp` | `operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"` |
| `telemetry/otel` | `telemetry` | `telemetry "github.com/maxtanforge/go-frame/telemetry/otel"` |
| `telemetry/metric/otel` | `metricotel` | `metricotel "github.com/maxtanforge/go-frame/telemetry/metric/otel"` |
| `telemetry/metric/redis`（v0.1.3） | `metricredis` | `metricredis "github.com/maxtanforge/go-frame/telemetry/metric/redis"` |
| `redis` | `redis` | `redisdb "github.com/maxtanforge/go-frame/redis"` |
| `cache/redis` | `redis` | `rediscache "github.com/maxtanforge/go-frame/cache/redis"` |
| `lock/redis` | `redis` | `redislock "github.com/maxtanforge/go-frame/lock/redis"` |
| `database/audit/principal` | `principal` | `auditprincipal "github.com/maxtanforge/go-frame/database/audit/principal"` |
| `app/config/redis` | `redis` | `redisconf "github.com/maxtanforge/go-frame/app/config/redis"` |

实际签名可使用 `go doc github.com/maxtanforge/go-frame/<组件路径>` 查看。

## 业务接线与所有权

| 需求 | 选择与责任 |
|---|---|
| 进程内对象缓存 | `cache/memory.New`，显式容量、LRU 和毫秒 TTL；创建方退出时 Close |
| 共享 Redis 对象缓存 | `redis.Open` / `OpenReady` 创建 Pool，`cache/redis.New(pool.Client, ...)` 借用；只有 Pool 的所有者负责关闭连接 |
| HTTP 输入和错误输出 | `binding.Query/JSON/Path/JSONWithPath/PathWithQuery` 组合声明式绑定、校验和响应；`request.Prepare` 与低层 Decode* 可独立使用；公开业务错误使用 `httperror.Error` |
| 健康检查 | `health` 计算报告，`http/health` 输出端点；router 已接入 HTTP 适配 |
| 表单、上传、XML | `binding.Form`、`PrepareMultipart`、`PrepareXML/XML`；[表单与流式上传](../http/request/FORM.md)、[XML 原始报文与响应](../http/request/XML.md)，[协议示例](../examples/protocols/README.md) |
| Redis 租约锁 | [lock/redis](../lock/redis/README.md) 借用客户端，持有者比对、续期、失锁取消；不承诺跨故障绝对互斥 |
| 定时任务与业务 worker | [schedule](../schedule/README.md) 提供毫秒间隔和显式时区 Cron；[background 示例](../examples/background/README.md) 组合 Task/Group 和业务消息确认 |
| 横切基础示例 | [toolkit 示例](../examples/toolkit/README.md) 是缓存（内存/Redis）、租约锁、并发准入、定时观察、出站 JSON 与中间件/trace 直用的真实调用点，通过小接口显式注入依赖 |
| 认证、操作日志和审计字段 | HTTP 认证注入 principal，操作事件显式配置 Sink，`database/audit/principal` 转换操作人；不自动执行权限或租户过滤 |
| 标准日志与链路 | logger 提供 slog，trace 提供请求关联 ID，otel 提供标准分布式传播；SDK/exporter 由业务构造和关闭 |
| 请求延迟指标 | metric 提供零依赖定桶直方图与 Prometheus 文本导出；OTLP 双写经 metricotel 可选绑定；标签只用路由模板与标准方法 |

资源按启动顺序装配、退出时按约定清理；不要让缓存适配与连接池重复关闭同一个客户端。
完整接线优先看[Ent 综合示例](../examples/ent/README.md)、[开发 CLI](../cmd/frame/README.md)、[独立业务模板](../templates/service/README.md)和[架构说明](architecture.md)。

## 当前目录边界

源码采用上述路径，不提供旧根层包的兼容目录。`internal/` 为内部实现；`examples/`
用于演示；`templates/service` 是需单独验证的独立 module；`cmd/frame` 提供开发工具；`scripts/` 提供本地验证与消费验收脚本。
框架不提供业务 Dockerfile、
注册中心、权限体系或自动租户隔离。缓存不自动做降级、两级联动或分布式失效。
