# 类型化入口声明对照

本目录是消费原型，不是新的框架公共 API。三个 [endpoint 助手](endpoint/endpoint.go) 仅调用现有 binding 工厂及 Route.Bind；输入准备、校验、错误传播与路由政策仍由现有机制负责。

两个模块使用同一组回调、Route 和 Options，以便逐请求比较 Routes / TypedRoutes 行为；分类的 TypedRoutes 仅存在于测试文件，生产服务不依赖实验助手：

- [分类对照](../business/internal/category/http/typed_test.go)：详情使用 Path，PATCH 使用 JSONWithPath；验证规范大 ID、字段白名单、note 省略/null/值及业务权限。正式声明见 [routes.go](../business/internal/category/http/routes.go)。
- [交易分页](transactionhttp/http.go)：使用 PathWithQuery；独立 path/query DTO，分页默认、边界和排序白名单；返回 pagination.Page 的五字段结果。Service 接收业务 Query，身份来自认证后的 context。

```go
// 原有公共 API
route.Bind(binding.Path(detail, opts))
route.Bind(binding.PathWithQuery(list, opts))
route.Bind(binding.JSONWithPath(patch, opts))

// 示例内的候选组合
endpoint.Path(route, detail, opts)
endpoint.PathWithQuery(route, list, opts)
endpoint.JSONWithPath(route, patch, opts)
```

两种写法仍需一份 Route 政策、一个有类型回调及一份共享 Options。助手将每个入口的两层工厂组合缩成一次调用，但不会减少业务 DTO、输入输出转换或政策字段。新增责任为三个泛型薄包装及对照维护；拆出具名回调、共用 opts 和 Route 值对可读性的改善，两种写法均可获得。

当前决定：保留显式类型化注册为主入口；助手留在示例内。两个模块证明这种组合可消费，但减少的仅是调用嵌套，尚不足以发布一套与 binding 平行的基础 API。后续若出现能减少政策重复、同时维持类型检查和明确错误的需求，再按独立设计推进。运行时 Meta 保持已有比较原型，静态生成和 proto 只有设计草图。

运行对照测试：

```powershell
go test ./examples/contracts/... ./examples/business/internal/category/http -count=1
```

整体边界见 [架构说明](../../docs/architecture.md)。没有做性能 benchmark，不声称助手改善请求吞吐或减少分配。Service 示例不演示真实数据库或事务，数据库 Executor 的所有权与选择继续由业务装配决定。
