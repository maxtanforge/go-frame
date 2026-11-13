# 公共审计字段

v0.1.3的 Actor 包含 Name 快照。audit/principal.FromContext 自动取得已验证用户 ID 和名称；操作日志自动留痕，实体审计字段由 Ent 持久化钩子自动填充，不额外写审计事件表。名称不作为权限依据，匿名 Actor 不携带 ID 或名称。

`database/audit` 只依赖标准库。它提供创建/更新元数据，不负责认证、权限、租户、数据库事务或完整审计历史，也不要求业务嵌入统一 BaseModel。

```go
actor := audit.Actor{Kind: audit.System, ID: "daily-import"}
fields, err := audit.New(actor, clock())
// 处理 err 后，与业务记录一起写入同一事务。
updated, err := fields.WithUpdate(actor, clock())
// 只把更新字段交给显式 UPDATE；created_at/created_by 保持不变。
```

领域和数据库时间统一为 **UTC、毫秒精度**，写入前截断，不四舍五入，Go 中保留 `time.Time`。HTTP 对外字段采用 **Unix 毫秒整数**：在响应 DTO 中显式将时间转换为 `respond.MillisTime`，或用 `UnixMilli()` 生成整数。直接 JSON 序列化 `audit.Fields` 会得到标准库的 RFC3339 文本，因此不要直接将该领域结构作为 HTTP 响应。records 的创建、详情和列表通过同一个 DTO 转换输出数字时间；audit 包不依赖 HTTP 包。

示例数据库使用 MySQL `DATETIME(3)`、PostgreSQL `TIMESTAMPTZ(3)`；共同支持的 UTC 年范围为1000–9999，零时间无效。

`New` 使用传入的同一个时间和操作人填充创建、更新字段。`WithUpdate` 返回副本，不修改原对象，保留创建字段；更新时间允许相等，拒绝倒退。时钟由业务传入，每次操作采样一次，框架没有全局时钟。时间比较不提供并发写入保护，乐观锁或数据库锁需要业务另行选择。

Actor 的 Kind 必须显式选择：

- `Subject`：业务主体，字符串 ID，不限定为整数。
- `System`：具名系统任务。
- `Anonymous`：明确匿名，ID 必须为空；是否允许匿名由业务决定。

Subject/System 的 ID 必须是有效 UTF-8、非空、无首尾空白和控制字符、最多128字节。Actor 是调用方提供的值，不证明身份可信，不能直接信任客户端身份头。错误统一为 `ErrInvalid`，不回显 ID。

Fields 的公开字段支持 SQL 扫描和明确的历史数据导入。导入者必须确认源时间所代表的时区，显式转换到 UTC/毫秒后调用 `Validate`；不会自动把历史创建时间改成“现在”。`Validate` 要求 canonical UTC 毫秒时间、合法 Actor、创建时间不晚于更新时间。`WithUpdate` 拒绝无效旧状态，不悄悄修复导入数据。

字段赋值、业务记录和必要的事件写入应由用例放在同一事务。records 示例在创建事务中使用同一 Actor/时间写记录与创建事件；该创建事件表不是完整的追加式操作历史。更新接口、审计历史保留策略、软删除和乐观锁不由这些值对象自动提供。
