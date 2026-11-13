# Snowflake IDs

`NewSnowflake(node)` constructs one process-local generator for node 0..1023.
Inject its `NextContext(context.Context) (int64, error)` capability into services.
Reuse the generator for all writes using that node. Do not create one generator
per request, per service feature or per insert. `NextContext` observes cancellation
while waiting for a new millisecond; detected clock rollback returns an error.

Rollback is deliberately fail-fast; no ID or sequence state is advanced on that
failure. The same generator resumes when the clock reaches its last issued
millisecond, continuing the sequence without duplicates. A tolerance window is
not enabled implicitly: callers decide whether to retry within their own deadline.

Framework configuration exposes `ID_NODE` (`id.node`), defaulting to 0. Service
templates use it for Snowflake request correlation IDs; records and Ent examples
share their generator between request and business IDs. Assign distinct nodes
to concurrent service instances. No node is derived from a random instance ID,
hostname, IP address or truncated hash.

For Kubernetes, assign distinct node numbers through deployment configuration;
a stable StatefulSet ordinal may be used only with a deployment-owned allocation
range that does not overlap other workloads. For VM/Docker, assign and track a
distinct ID_NODE per active generator. Replica counts and allocation must stay
within the 1024-node layout. No registry or automatic lease allocator is provided.

Distinct nodes plus suitable clocks are deployment prerequisites, not verified
globally by this library. Do not run two generators with the same node concurrently.
Restart resets local timestamp/sequence state: a reused node must not issue IDs
in a timestamp range already used by its predecessor. Ensure the predecessor is
stopped and clock/restart policy handles this; local rollback detection alone
does not persist a high-water mark across processes. IDs are roughly time ordered
across nodes, not a globally strict ordering of transactions or commit times.

Records stores signed BIGINT/int64 values and exposes IDs as decimal JSON strings
using `json:"id,string"`, preserving JavaScript precision. Path parsing retains
the full int64 value. Failed ID generation enters no database transaction and is
not silently retried or replaced with another ID algorithm. This integration does
not add cursor pagination or claim exactly-once business writes.

## 规范 ID 值（v0.1.3）

`id.ID` 是非负 int64 的传输值模型，不参与生成。`id.Parse` 只接受规范十进制文本：拒绝前导零、正负号、空白、指数、小数和 int64 溢出。JSON 始终输出字符串，读取也必须是字符串，保持 JavaScript 精度。零有效；业务通过 `v:"gt=0"` 等规则要求正数。

DTO 字段直接使用 `id.ID`；数据库/领域调用通过 `value.Int64()` 取得 int64。需要业务命名时使用 `type ID = id.ID` 类型别名；重新定义 `type ID id.ID` 不继承框架的 ID 绑定语义。Prepared/binding 在 JSON、query、params、form 中执行同一规范，支持嵌套、切片、指针与 optional.Field；无需业务层再次遍历或重新反序列化。低层 DecodeJSON/DecodeQuery/DecodePath 也支持该值类型。
