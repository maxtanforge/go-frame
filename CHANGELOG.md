# 更新日志

## [v0.2.6]

go-frame 在 GitHub 上的首个正式 Release。

- Go 模块路径为 `github.com/maxtanforge/go-frame`。
- 提供显式服务装配、启动探活、失败回滚和协作式停机，以及按需接入的 HTTP、SQL、Redis 和观测能力。
- 出站 HTTP 客户端提供分阶段错误和响应体超限标识。
- 服务模板和业务示例覆盖配置预检、分类 CRUD、档案 PATCH、设备同步及事务接线。

- 修复日志轮转占位文件与后台归档扫描并发时的偶发错误。
- 分开执行 race 功能验证与普通构建的分配预算测试。
