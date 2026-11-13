# Cache

`cache.Store` is a byte cache contract with memory and Redis implementations. `Get` returns `(value, hit, error)`; a present nil or empty value is a hit. Missing keys are not errors. Backend errors are returned without fallback, retries added by this package, or load-through behavior. Configure any underlying Redis client retries separately.

Keys contain 1–512 bytes (UTF-8 byte length, not rune count). Values default to a maximum of 1 MiB; both adapters accept `cache.Options{MaxValueBytes: n}`. Zero selects the default and negative limits are invalid. TTL is zero for persistent entries or a positive whole number of milliseconds; negative and fractional-millisecond durations are rejected instead of rounded. Delete is idempotent for absent keys. Callers must provide a non-nil context. Context cancellation is checked before work; operations already completed are not rolled back.

## Memory

```go
c, err := memory.New(memory.Config{Capacity: 1000})
if err != nil { return err }
defer c.Close()
err = c.Set(ctx, "profile:42", []byte(`{"name":"Ada"}`), time.Minute)
```

Memory uses Hashicorp `golang-lru/v2` v2.0.7. Capacity is a mandatory positive **entry count**, not a total byte budget. Each entry has the configured value bound; keys and LRU bookkeeping add memory overhead. Reads update recency. Reads and writes copy bytes so callers cannot mutate stored data. Instances are independent and safe for concurrent use. A single mutex protects expiry and LRU changes.

Expiry is lazy: a read at or after the deadline removes that entry and reports a miss. Other expired entries may stay resident until they are read, replaced, evicted, or the cache closes. A write does not sweep expired entries; ordinary LRU eviction can evict a live least-recently-used entry even if another entry has expired. There are no background workers. `Config.Clock` supports deterministic time in tests; it runs under the cache mutex, must return promptly, and must not call back into the cache. Replacing a key replaces its deadline, including clearing it when TTL is zero.

`Close` is idempotent, clears retained entries, and makes later Get/Set/Delete operations return `cache.ErrClosed`. Close does not belong to `Store` because ownership is adapter-specific.

## Redis

```go
c, err := rediscache.New(client, rediscache.Config{Namespace: "billing:v1"})
```

The adapter borrows a go-redis client through a three-command interface (`Eval`, `Set`, `Del`). The application owns its lifecycle and must close it separately. The adapter has no Close method and never runs KEYS or FLUSHDB. A namespace of 1–512 bytes without surrounding whitespace is mandatory; physical keys are `namespace + ":" + logicalKey`. Reserve non-overlapping namespace/key combinations across applications (a namespace containing a colon is not an independently enforced Redis boundary).

Reads use an atomic Lua script that checks STRLEN before GET, so a foreign oversized value is not downloaded. Oversized values report `(nil, true, cache.ErrValueTooLarge)` and remain unchanged. Wrong-type and other Redis errors are returned unchanged; only `redis.Nil` becomes a miss. Redis ACLs must permit EVAL plus STRLEN and GET. Writes use SET with the supplied TTL; zero clears an earlier expiry. Redis handles expiry itself. The same client can be shared with unrelated Redis capabilities.

There is no cluster-wide invalidation protocol, two-level cache, lock, stampede prevention, or automatic memory fallback. Memory and Redis are explicit alternatives selected by the application.

## JSON

`cache.GetJSON[T](ctx, store, key)` returns `(value, hit, error)`. JSON `null` is a hit; use a pointer type to distinguish null from a scalar zero value. A decode error also preserves `hit=true` and returns the zero value of T. Store errors remain discoverable through `errors.Is`/`errors.As`. `SetJSON` returns marshal errors before invoking the backend.

Both helpers accept one optional `cache.Options` value and default to 1 MiB independently of adapter configuration. GetJSON checks bytes before decoding; SetJSON checks serialized bytes before writing. The limit does not bound temporary allocations made by `json.Marshal`, custom marshalers, or the application value itself. Align helper and adapter limits when using a non-default maximum. These helpers do not fetch a source of truth or mask cache failures.

Tests use an injected memory clock and Redis command fakes, without network listeners. They verify the Redis command boundary, not a live Redis server's Lua engine or ACL configuration.

## 业务键与失败政策

按业务域统一维护 namespace、逻辑 key 和 TTL，例如 Redis namespace 为 `billing:v1`，逻辑 key 为 `profile:42`，物理 key 为 `billing:v1:profile:42`，TTL 为一分钟。namespace 的版本表示缓存数据格式；变更结构时显式选择新版本，旧键依靠已设置的 TTL 退出。它不是权限或租户隔离机制，鉴权应在读取缓存结果前完成。

业务层集中维护这些常量与 key 构造规则，将用户输入转换成业务认可的标识后再构造键。不要为每次读取新建 Redis 连接；借用同一个 Pool.Client 构造适配器并注入用例。`examples/toolkit.NewRedisCache` 使用示例 namespace `toolkit`；真实业务可直接调用上面的 `rediscache.New` 选择所属域。

| 读取或更新结果 | 业务处理 |
|---|---|
| `hit=true, err=nil` | 使用缓存值；空值与 JSON null 都可能是有效命中 |
| `hit=false, err=nil` | 查询权威来源；成功后按业务 TTL 回填，不缓存加载失败 |
| 后端失败或 JSON 解码失败 | 明确选择返回失败或回源，并记录诊断；不能悄悄当作普通 miss |
| 回填失败 | `toolkit.Cached` 返回错误；允许返回源数据的业务必须另行明确该政策 |
| 数据库更新成功 | 提交后删除关联键并处理删除错误；删除失败不能撤销已提交事务 |

并发 miss 会分别回源，没有同键合并。提交后删除也不能阻止一个更早开始的读请求回填旧值；短 TTL 只能限制过期时间，不能提供强一致。负缓存、多副本失效、重试和本地回退都需业务明确选择，缓存不能替代数据库约束。

## 与框架资源接线

本地缓存导入 `github.com/maxtanforge/go-frame/cache/memory`，由创建方在退出时 Close。
Redis 连接由 `redis.Open` 返回的 Pool 持有；将 `pool.Client` 传入
`cache/redis.New`。关闭 Pool 的责任仍在 app/resource 的创建方，缓存适配不重复关闭。

```go
import (
    "github.com/maxtanforge/go-frame/cache"
    "github.com/maxtanforge/go-frame/cache/memory"
    rediscache "github.com/maxtanforge/go-frame/cache/redis"
)

// 选择所需实现，再显式传入用例；不是同时自动开启两级缓存。
local, err := memory.New(memory.Config{Capacity: 1000})
remote, err := rediscache.New(pool.Client, rediscache.Config{Namespace: "billing:v1"})
var store cache.Store = local // 或 remote
```

示例片段省略了外围函数；实际接线须分别检查构造错误，给本地 cache 注册 Close，
并给 Redis Pool 注册启动探活与关闭。缓存不是业务数据的唯一来源。
