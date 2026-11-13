# 设备事件接收与恢复

本例由 SCRM 的设备同步调用方式提炼，演示业务如何组合 SQL、生命周期和并发预算。
它是业务拥有的代码，不是框架的通用任务系统。默认分类服务不会挂载这些入口或创建表。

| 文件 | 职责 |
| --- | --- |
| [service.go](service.go) | 不依赖 HTTP/SQL 的事件、去重契约和单次投递用例 |
| [store/postgres.go](store/postgres.go) | PostgreSQL 持久化接收、完成标记和有界待处理查询 |
| [store/schema.sql](store/schema.sql) | 需显式应用的示例表与待处理索引 |
| [worker/worker.go](worker/worker.go) | 有界内存调度、共享执行预算和停机排空 |
| [composition_test.go](composition_test.go) | 满载、停机、新服务重放的组合验收 |

## 接收与完成分别表示

1. 可信平台适配器验签并确认用户归属，构造稳定的来源／用户范围内事件 ID。
   每次回调不能重新生成 ID。`Accept` 提交事件；相同 ID、不同内容返回 `ErrConflict`。
2. `Delivered=false` 表示已持久化、待投递。调用 `Worker.Submit` 提交该 ID，成功也只表示进入内存队列。
   队列满或已关闭时，SQL 中的待处理记录仍保留；HTTP 适配应如实报告已接收／待处理，不能报告已完成，
   也不能把队列失败当成数据库已回滚。提交结果不明时返回错误，调用方使用同一 ID 重试核对。
3. `Deliver` 从存储加载原始内容，调用 `Sink.Save`，仅在成功后标记完成。
   网络请求不占用 SQL 事务。调用失败或取消时不报告完成。完成标记结果不明时，
   下一次读取持久化状态，决定跳过已完成事件还是使用原 ID 重放。
4. 启动恢复或显式补偿时，使用 `Store.Pending(ctx, limit)` 查询至多 100 个 ID，再逐个提交。
   遇到满载停止本批提交，下一次从持久化状态继续。业务需要为长期失败记录定义退避、公平性、
   告警、保留期限及人工处理；本例不启动无限重试循环或自动清理记录。

并发投递和“远端成功、本地确认失败”都可能造成重复调用。因此接收端必须按稳定事件 ID
原子去重并校验内容，而不仅是接受一个 `Idempotency-Key` 请求头。本例提供至少一次尝试的组合方式，
不承诺 exactly-once；没有接收端幂等契约时，必须另外设计核对或补偿。

## 生命周期与资源所有权

`worker.New(capacity, sharedExecutor, service.Deliver, report)` 不启动 goroutine。
把返回的 Worker 注册到 `app.Group`，由 Group 调用阻塞的 `Run`。队列容量上限为 1024，
单个 Worker 串行处理；多个 Worker 可借用同一个 Executor 限制同一下游的总并发。
`Submit` 不等待容量，也不会先启动 goroutine 再等待许可。

正常 `Shutdown` 关闭准入并在预算内处理已排队 ID；超时或 `Close` 取消当前处理。
处理函数必须响应 context。纯取消按正常停机处理，独立错误和 panic 保留诊断。
进程退出后不依赖内存队列恢复；未完成记录通过上述持久化查询重放。
先停止 HTTP 接收，再停止 Worker，最后由组合根关闭共享 Executor、HTTP transport 和数据库。

后台投递明确使用生命周期 context；请求 context 只控制接收及提交动作，不保存到任务对象。
`Result` 回调用于记录单次结果，必须快速且在共享时并发安全；事件 ID 不应成为指标标签，
未经过安全映射的错误原因不得直接返回给客户端。

## 验证

从仓库根目录运行：

```sh
go test ./examples/business/internal/devicesync/... -count=1
go run ./scripts/consumer --repo . --mode workspace
```

覆盖重复与冲突输入、远端失败恢复、完成标记失败重放、取消、队列满载、关闭竞态和预算内排空。
PostgreSQL 适配测试使用 sqlmock 验证事务及查询契约；没有连接真实数据库，
也不代表已验证平台签名、远端幂等实现或生产吞吐。外部消费检查会复制整个 business 示例并重新测试。
