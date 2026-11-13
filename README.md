# go-frame

go-frame 是 Go 服务基础库。业务通过构造函数装配 HTTP、生命周期、数据库、Redis 和观测能力，并持有自己的业务规则和部署配置。路由使用 chi，日志使用 slog，数据库层支持按需接入 Ent 等 ORM。

最低 Go 版本以 `go.mod` 为准，当前版本为 `v0.2.6`。GitHub 可下载版本以实际标签为准，见[版本发布](docs/releases.md)。

## 文档

| 内容 | 入口 |
|---|---|
| 按任务选择示例和组件 | [文档导航](docs/README.md) · [示例导航](examples/README.md) |
| 用法、限制和代码片段 | [能力与使用参考](docs/reference.md) |
| 设计取舍、运行流程与包边界 | [设计说明](docs/design.md) · [整体架构](docs/architecture.md) · [能力目录](docs/packages.md) |
| 新建服务和接入现有业务 | [服务模板](templates/service/README.md) · [业务装配](docs/business-integration.md) |
| 本地开发与测试 | [开发指南](docs/development.md) · [验证指南](docs/verification.md) |
| 分支、版本标签和 GitHub Release | [版本发布](docs/releases.md) · [更新日志](CHANGELOG.md) |
| 部署时的配置、日志和生命周期 | [运行约束](docs/operations.md) |

## 快速开始

| 你要做什么 | 推荐入口 |
|---|---|
| 理解启动和运行机制 | [basic](examples/basic/main.go)：可直接运行，SQL/Redis 默认关闭 |
| 创建独立业务工程 | [服务模板](templates/service/README.md)：独立 module、bootstrap 和功能目录 |
| 参考完整 CRUD 与业务分层 | [分类业务](examples/business/README.md)：DTO、VO、Handler、Service、Store 与事务；[运行步骤](examples/business/RUNNING.md) |

```sh
go run ./examples/basic
curl http://localhost:3000/healthz
curl -H 'Content-Type: application/json' -d '{"name":"Ada"}' http://localhost:3000/greetings
```

未配置 `DATABASE_URL` 或 `REDIS_ADDR` 时也可以运行 basic 示例。启用依赖后，应用会在开始接收请求前检查连接。

## 本地验证

从根目录执行 `go test ./... -count=1`。独立服务模板、race 和集成测试的入口见[验证指南](docs/verification.md)。

## 许可证

[MIT](LICENSE)
