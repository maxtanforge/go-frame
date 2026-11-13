# 版本发布

## 分支与版本

日常改动通过 PR 合入 `dev`。准备发布时将验证过的 `dev` 合入 `main`。
普通分支推送不会触发 Release。

以根目录 `VERSION` 为版本声明。调整版本时同步服务模板依赖和文档声明，
并在[更新日志](../CHANGELOG.md)添加非空的 `## [vMAJOR.MINOR.PATCH]` 条目。
由[版本一致性检查](verification.md#发布一致性)核对。

## 发布流程

1. 在待发布提交上执行 `go run ./scripts/release-check`，并按[开发指南](development.md#工具取舍及验证)完成 race 和独立消费验证。
2. 将待发布提交合入 `main`，在对应提交上创建与 `VERSION` 一致的稳定版本标签（`vMAJOR.MINOR.PATCH`）。
3. 在该提交上运行 `go run ./scripts/release-check --tag`，然后只推送这一个标签。
4. 在 GitHub Actions 查看 Release 工作流的验证和发布结果。

[Release 工作流](../.github/workflows/release.yml)核对标签、版本及提交是否属于
`main` 的历史，执行 `sh scripts/verify.sh --race`，再用
`go run ./scripts/consumer --repo . --mode workspace --network --race --budget 10m`
验证独立消费工程。CI 允许下载缺失依赖，框架本身仍通过本地 `replace` 使用被标记的源码。通过后使用
GitHub 内置令牌创建 Release，发布说明来自标签提交中对应版本的更新日志。

## 发布后验收

按[独立消费工程验收](development.md#独立消费工程验收)使用刚发布的标签执行无本地
`replace` 的远程验证。GitHub 标签与 Release 是两个状态。标签推送后 Go 即可尝试
读取该版本，工作流失败时标签仍存在。先查看失败步骤；若需修正已供消费的代码，
使用新版本发布。
