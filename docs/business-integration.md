# 业务接入顺序

业务代码采用 feature/service/store 分工，入口显式持有配置、日志、连接池与后台服务。
以下是一条推荐接入路径；可执行例子保留在各自包，按需组合，不强迫每个业务引入所有驱动。

企业项目的开发规范可以作为接入参考：统一入口声明、用例输入输出、错误码、数据访问和缓存键，比单纯增加组件更能减少重复工作。依赖仍显式装配，业务规则、公开接口与事务范围由应用决定。

目标是统一基础架构体系：业务主要实现规则和用例，框架承担连接生命周期、输入机制、事务收尾和通用诊断。启动层显式创建和注入 SQL、Redis、ID、时钟、日志等依赖；用例接收自身定义的最小能力接口。具体接线见 [事务消费示例](../examples/transactions/README.md)，其双写仓储共用 `database.Executor`，明确区分业务命令与 HTTP DTO。

| 阶段 | 装配与验证 |
|---|---|
| 工程与配置 | 从 [独立模板](../templates/service/README.md) 创建业务 module；启动前运行配置检查，日志通过 app/startup 管理，环境和密钥归业务部署 |
| 数据与缓存 | 在 resource Hook 创建/探活 SQL、Redis，创建 Ent client 和审计 hook；缓存及锁借用 Redis，最后只由 pool 所有者关闭 |
| 身份与路由 | 参考 [认证示例](../examples/authenticated/README.md)，公开接口精确声明，其余需认证；HTTP principal 转为领域 actor，操作日志 Sink 显式配置 |
| DTO 与用例 | Query/JSON/Path/Form 使用 binding；XML、Multipart 用专门入口。服务接收 context 和业务值，校验错误完整保留，HTTP 只返回首条安全消息 |
| Ent 与事务 | 参考 [Ent 示例](../examples/ent/README.md)，实体规则共享纯值检查；系统审计字段在 hook 中填写，写字段白名单在业务映射，跨实体写显式 WithTx |
| 回调与文件 | [协议示例](../examples/protocols/README.md) 展示 XML 原文验签入口与上传暂存清理；公开平台回调仍必须验签，返回确认前完成必要持久化 |
| 定时任务与队列 | [background](../examples/background/README.md) 在启动时注册 Task/Group/schedule；处理函数归业务，锁可选，队列确认与死信由业务协议适配 |
| 停机 | 先停止新请求/调度/消息拉取，取消或等待在途业务，在预算内确认消息，随后关闭数据库、Redis 和日志；不得由借用方重复关闭连接 |

XML 使用标准 xml 标签映射和值校验；缺失/默认值规则与 JSON 不同，详见能力文档。上传文件在完整 DTO 校验前只暂存，认证和配额应先于读取。操作日志不要默认记录 XML 原文、上传字节、令牌或敏感字段。

后台任务没有 HTTP principal，应由业务显式选择系统 actor。调度器事件记录运行状态，不等同于业务审计历史。周期去重和消息幂等通常需要数据库唯一约束；租约锁不能替代持久化幂等设计。

验证业务接入时，按实际采用的能力保留少量认证与输入失败、事务提交/回滚、上传清理和后台停机的组合验收；再对实际部署的 MySQL/PG（二选一）、Redis、队列和平台回调做集成验收。仓库单元测试不会访问生产服务。业务测试不需要逐接口重复框架解码与响应机制的完整矩阵。

## 普通业务入口：有类型的处理函数与显式路由

binding 已提供 Query、JSON、Path、JSONWithPath。普通入口优先使用这些能力，在注册时准备输入和字段规则，业务回调返回明确类型；不必逐接口手写 Decode → Validate → Adapter。构造错误返回启动装配点处理。

下面是分类详情入口的完整声明示例。Category 是业务查询结果，categoryResponse 是 HTTP 表示；服务接口和主体转换函数由业务定义，框架不假定主体 ID 一定是整数。

```go
package categoryhttp

import (
    "context"
    "errors"
    "net/http"
    "strconv"
    "time"

    "github.com/maxtanforge/go-frame/http/binding"
    "github.com/maxtanforge/go-frame/http/httperror"
    "github.com/maxtanforge/go-frame/http/respond"
    "github.com/maxtanforge/go-frame/http/router"
    "github.com/maxtanforge/go-frame/validation"
    "github.com/go-chi/chi/v5"
)

type Category struct {
    ID        int64
    Name      string
    CreatedAt time.Time
}

type DetailService interface {
    Detail(context.Context, int64, int64) (Category, error)
}

var ErrCategoryNotFound = errors.New("category not found")

type ActorID func(context.Context) (int64, error)

type categoryPath struct {
    ID string `path:"categoryId" v:"required"`
}

type categoryResponse struct {
    ID        int64              `json:"id,string"`
    Name      string             `json:"name"`
    CreatedAt respond.MillisTime `json:"createdAt"`
}

func Routes(service DetailService, actorID ActorID) ([]router.Route, error) {
    if service == nil || actorID == nil {
        return nil, errors.New("category HTTP: service and actor resolver required")
    }
    return router.Collect("category", router.Route{
        Method: http.MethodGet, Path: "/api/v1/categories/{categoryId}",
    }.Bind(binding.Path(
        func(ctx context.Context, in *categoryPath) (*categoryResponse, error) {
            id, err := strconv.ParseInt(in.ID, 10, 64)
            if err != nil || id <= 0 || strconv.FormatInt(id, 10) != in.ID {
                return nil, &validation.FieldError{
                    Field: "categoryId", Rule: validation.RuleFormat,
                }
            }
            actor, err := actorID(ctx)
            if err != nil {
                return nil, err
            }
            result, err := service.Detail(ctx, actor, id)
            if err != nil {
                return nil, err // 未识别的原因保留内部诊断，不公开原文
            }
            return &categoryResponse{
                ID: result.ID, Name: result.Name,
                CreatedAt: respond.MillisTime(result.CreatedAt),
            }, nil
        },
        binding.Options{MapError: categoryError, PathParams: func(r *http.Request) map[string]string {
            return map[string]string{"categoryId": chi.URLParam(r, "categoryId")}
        }},
    )))
}

func categoryError(err error) error {
    if errors.Is(err, ErrCategoryNotFound) {
        return &httperror.Error{
            Status: http.StatusNotFound, Code: 3001,
            Message: "分类不存在", Err: err,
        }
    }
    return err
}
```

示例对“规范正整数 ID”有业务要求，因此路径先保留原始字符串，再拒绝 `01`、`+1` 等格式。若业务只要求正整数，可使用 int64 路径字段配合 v:"gt=0"。框架的整数解码不能代替业务的规范字符串协议。

需要让格式错误与其他字段诊断一起收集时，可以使用已有的 DTO 自定义校验，而不新增全局校验规则：

```go
func (p categoryPath) Validate(c *validation.Context) {
    if !c.Valid("ID") {
        return
    }
    id, err := strconv.ParseInt(p.ID, 10, 64)
    if err != nil || id <= 0 || strconv.FormatInt(id, 10) != p.ID {
        c.Add("ID", "format", "分类 ID 格式不正确")
    }
}
```

这个方法是前面路径格式分支的可选放置方式：采用后，回调只做字符串到业务 ID 的转换并处理转换错误，不再重复格式规则。Context 使用 Go 字段名 ID，框架将其映射为外部 categoryId。多个业务入口需要同一政策时，可共享业务纯值函数；是否需要新的框架标量转换能力，要另外证明收益。

Routes 返回的声明由组合根交给认证路由装配；actorID 从已验证 context 映射业务主体，转换失败返回错误。Service 仍负责分类存在性、账本权限与业务状态，不能把 v 标签当作授权。示例为独立编译把声明集中在一个包；实际工程的实体和领域错误定义在业务包，HTTP 适配引用它们，业务不反向导入 HTTP 包。示例没有数据库实现，也没有改变 ledger 的现有接口。

当前接入使用显式注册：`Route.Bind` 接收 handler 构造结果，`router.Collect` 返回整批声明或包含模块、方法、路径的错误，并保留原始 cause。[frame 示例](../examples/transactions/README.md) 比较了显式路由与启动期 Meta 工厂字段扫描；该扫描器仅是示例原型，尚未提供框架 Meta 或 gRPC API。HTTP 和未来 gRPC 可分别适配到同一业务 Input / Result；身份、错误与 PATCH 表示需按协议明确映射，不能仅加标签就视为完成跨协议迁移。

`Collect` 与 `NewAuthenticated` 会拒绝同方法下等价模式的覆盖，例如 `/categories/{id}` 与 `/categories/{name}`；模块分别构造后，合并声明仍须交给 `NewAuthenticated` 检查。不同方法、不同正则及静态路径重叠保留 chi 匹配规则；检查不判断任意正则的交集，也不替代业务权限审查。

JSON 创建入口采用相同模式：独立请求 DTO → 显式业务命令 → Service → 响应 DTO。PATCH 用 optional.Field 保留三态，规则与目录示例见[建模指南](modeling.md)。完整可运行的创建、查询、PATCH 和删除参考 [Ent HTTP 示例](../examples/ent/http.go)。

需要将业务类型与 HTTP 完全隔离时，参考[分类业务契约示例](../examples/business/README.md)：领域 ID 解析、权限和状态规则在业务包，路径和 PATCH DTO 在适配包，已知领域错误在适配边界映射，未知存储原因保留内部诊断。PATCH 的省略、null、空字符串分别转换为保持、清空、设置，不能直接用 `*string` 丢失三态。错误映射使用 `errors.Is`，不按文本识别数据库错误；取消和超时保持原有 context 原因。

同一模块的类型化入口可复用 `binding.Options{MapError: httpError}`。回调直接返回业务错误，binding 只对这些错误调用 mapper；输入解码、字段校验和构造错误不经过它，取消与 deadline 错误保持原语义。mapper 返回 nil 不能吞掉失败；映射没有保留原始 cause 时，binding 会补足 cause。低层 Prepared / Adapter 流程仍在业务阶段显式调用本模块的映射函数。

### 专有协议保留明确的处理次序

| 场景 | 当前使用方式 |
|---|---|
| 单一路径、查询或 JSON 输入 | binding.Path / Query / JSON |
| 路径与 JSON，校验成功后再执行业务 | binding.JSONWithPath，字段错误可合并 |
| 路径解析后、正文失败前就要设置审计资源 | 显式 Prepared / Adapter 流程；JSONWithPath 的业务回调不会在正文失败时运行 |
| 路径与查询组合 | binding.PathWithQuery，路径和查询使用独立 DTO，按来源顺序合并字段错误 |
| 同步、识别、流式或平台回调 | 按协议组合低层工具，保留错误码、验签、限额与提交顺序 |

经过 HTTP 契约测试的分类创建参考见 [业务示例](../examples/business/README.md)：注册时准备路径与正文规则，有效父路径先设置 book 资源，正文或业务失败保留它，创建成功才切换到 category。仅挂载审计元数据不能代替 recorder.Middleware；资源设置错误应明确处理。记录 HTTP 执行过程不代表数据库事务提交或审计持久化。

同一入口只设置一个字段校验所有者。binding 已组合解码和标签校验，不在回调中重复 Validate；低层 Decode 与业务规则的职责以对应 API 文档为准。自定义 DTO 校验、原始格式规则和跨字段约束迁移时需单独核对。

Prepared JSON 使用声明式字段绑定，不能假定会执行 DTO 的自定义 UnmarshalJSON。旧入口通过它检查业务协议或返回特定错误码时，应保留兼容流程，或明确迁移到 DTO 自定义校验 / Service 并验证失败顺序。JSON 重复键、未知字段、UTF-8、根 null 与资源限制也应逐项对照当前业务契约，不能仅凭“严格 JSON”名称判定两种入口完全等价。

## 数据访问与缓存接入

数据访问使用业务生成的 Ent 客户端或显式 SQL Executor，不把业务查询藏在资源门面中。下面的入口已经存在，按用例选择即可：

| 需求 | 可运行参考 | 业务需要明确的内容 |
|---|---|---|
| 查询与过滤 | [Ent Service.Find / List](../examples/ent/service.go) | 原生 predicate、权限与可见范围；HTTP 响应由适配层选择公开字段 |
| 分页与排序 | 同一示例的 NewSorter / QueryPage | 排序字段白名单和稳定的唯一排序；Count 与列表不是自动一致性快照 |
| PATCH | 同一示例的 Service.Patch | 省略保持、null 清空、零值写入；仅显式声明的字段参与更新 |
| 跨操作事务 | 同一示例的 Create / Patch / Delete 与 writeEvent | 所有写入使用 tx 或 tx.Client()，不能在回调中改用根客户端 |
| 错误分类 | 同一示例的 ErrorClassifier 与 mapNotFound | 业务决定公开代码；原始 cause 保留，未知存储错误不能伪装成输入错误 |
| 原始 SQL | [frame 双写示例](../examples/transactions/README.md)、[records 存储](../examples/records/internal/sqlstore/store.go) | 参数化、显式 Executor、Rows 关闭与扫描错误；事务不通过 context 自动传播 |

事务成功后才能使关联缓存失效；缓存删除失败不意味着数据库事务可以回滚。需要可靠跨副本失效时，由业务选择持久化事件或其他一致性机制。SQL 事务不会覆盖 Redis 写入，也不自动提供跨存储原子性。

缓存接线与业务键、TTL 和故障政策见 [cache 业务用法](../cache/README.md#业务键与失败政策)。已有 [toolkit.Cached](../examples/toolkit/coordination.go) 展示显式读穿透，缓存后端错误和回填失败直接返回，不自动降级。需要回源优先的业务应自行声明并观察降级政策。

## 从真实业务边界选择组合

v0.2.2 新增的两个[业务示例](../examples/business/README.md)不默认挂载到分类服务，
可以通过测试独立运行，并被 `scripts/consumer` 复制到仓库外重新验收：

| 场景 | 可执行参考 | 需要保留的业务契约 |
| --- | --- | --- |
| 档案更新 | [profile](../examples/business/internal/profile/README.md) | 每次检查操作者与目标用户；授权依赖失败不能当成无权；嵌套 PATCH 保留字段存在性；档案与变更记录一起提交 |
| 强类型下游调用 | 同一示例的 upstream 适配 | 凭据按请求取得；区分传输、状态、协议与业务拒绝；完整调用结果覆盖正文读取、解码和业务信封校验 |
| 设备同步 | [devicesync](../examples/business/internal/devicesync/README.md) | 先持久化再调度；已接收不等于完成；失败重放使用原事件 ID；接收端原子幂等由业务确认 |
| 后台准入与停机 | 同一示例的 worker 适配 | 有界提交，不为每次请求先建 goroutine；关闭准入后在预算内排空，超时取消；未完成工作从持久化状态恢复 |

出站阶段判断使用 `errors.As` 获取 `httpclient.OperationError`；响应超限使用
`errors.Is(err, httpclient.ErrResponseTooLarge)`。HTTP 状态仍使用 `StatusError`。
这些 API 从 v0.2.2 起提供，接入前确认业务所锁定版本；业务 code 和是否重试仍由客户端适配定义。
避免按错误文本判断，也不要将上游完整响应或解包后的敏感原因直接返回给用户。

设备示例没有自动补偿调度器。上线前由业务补齐重放退避、公平性、保留期限及告警，
并验证真实数据库和远端幂等协议。SQL mock 和进程内恢复测试不代表这些外部契约已经成立。

## 业务测试与框架信任边界

| 测试对象 | 所有者 |
|---|---|
| 通用解码、标签执行、响应封装、错误映射和认证路由机制 | frame；业务采用这些入口后不逐接口重演 |
| 金额、日期、权限、状态机、幂等、并发和跨存储事务 | 业务 Service / 值规则与必要的数据库集成测试 |
| 哪些 PATCH 字段能清空、必填零值、ID 协议、自定义 DTO 规则 | 业务输入契约；集中表驱动测试，不必全部启动 HTTP 服务 |
| 公开端点名单、操作 action/resource、身份映射与模块是否挂载 | 应用装配契约；少量集中检查与代表性 HTTP 冒烟 |

HTTP 或 route 测试文件名不代表测试多余。例如注销命令是否私有、状态能力端点是否公开，是业务的授权配置，框架无法知道正确名单。业务自己实现的输入兼容层也仍需验证，直到被框架能力替换并确认语义一致。


## 先跑完整接入链路

[Ent 综合示例](../examples/ent/README.md) 在同一组业务端点组合默认认证、DTO、Ent 事务、
身份审计、操作日志和路由指标。专项示例仍供按需选择组件。系统任务应显式提供系统 actor，
HTTP 身份不能作为后台默认身份；用例不必引入令牌或 HTTP 类型。

独立工程参考服务模板，由业务或 AI 调整 module 和导入路径，明确选择框架版本／提交。
先检查代码和部署配置，再由业务工程解析依赖。
开发重启和配置检查见 [开发指南](development.md)，开发工具见 [CLI](../cmd/frame/README.md)。

使用 AI 编写业务时，按[AI coding 指南](ai-coding.md)选择对应版本的 API 与示例，
明确接口语义，再用业务测试验证；无需依赖功能骨架生成器或先建立 OpenAPI 模型。
