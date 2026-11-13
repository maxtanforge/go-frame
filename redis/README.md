# Redis lifecycle

`Open(Options)` validates options and creates a lazy client without dialing. It
requires `MinIdleConns: 0`. Use `OpenReady(ctx, Options)` inside a resource startup
callback when prewarming is desired; it enables the configured minimum idle
connections and checks connectivity with Ping. It does not wait for the entire
minimum idle target to become established. Both constructors reject pool limits
above 2147483647 before calling the driver.

Give OpenReady a startup deadline. A failed startup returns nil and closes its
client. On success, the application owns the Pool and must close it from its
resource Stop callback. Close is idempotent.

The Ping respects the context deadline, but go-redis also creates and initializes
connections using background contexts. An in-flight background dial or handshake
can outlive the startup call until its configured timeouts expire; closing the
client does not forcibly terminate all background work. Configure bounded dial,
read, write and pool timeouts. Cancellation during socket I/O may be observed at
the deadline or socket timeout rather than immediately.

Run the normal loopback-only tests with `go test ./redis`. To opt in to a real
Redis instance, set `FRAME_TEST_REDIS_ADDR` (and optionally
`FRAME_TEST_REDIS_USERNAME` / `FRAME_TEST_REDIS_PASSWORD`) and run
`go test ./redis -run TestRedisIntegration -count=1`. This test creates one unique
`go-frame-integration:` key with a short TTL, checks SET/GET/TTL and cancellation,
and deletes its own key. It never flushes a database. No live Redis service is
required for the default test suite.

## Fixed-window counters

`NewWindowCounter(pool.Client, CounterOptions{Namespace: "login"})` borrows the
native single-primary client. The caller continues to own and close the pool.
Automatic command retries must be disabled: `Open` disables them by default;
raw go-redis clients need `MaxRetries: -1` and `ContextTimeoutEnabled: true`.
The constructor rejects clients with normalized retries enabled or context
deadline support disabled. Do not mutate client options after construction.

```go
counter, err := redis.NewWindowCounter(pool.Client, redis.CounterOptions{
    Namespace: "login",
})
if err != nil {
    return err
}
count, err := counter.Increment(ctx, "user:42", time.Minute)
if err != nil {
    return err
}
// The business decides whether count.Count exceeds its limit.
// count.TTL is the remaining expiration observed during the atomic increment.
```

The first hit creates `namespace:key` with count 1 and an expiration. Later hits
increment without extending that expiration, even if they pass a different
window. An expired key starts again at 1. Valid existing counts without an
expiration receive the supplied window before incrementing. Counts remain exact
through the full positive int64 range, including values above 2^53.

Namespaces contain 1–128 ASCII letters, digits, dots, underscores or hyphens;
keys contain 1–512 valid UTF-8 bytes without whitespace or control characters.
Windows must be positive whole milliseconds. Nil or already canceled contexts
are rejected before any Redis command. Existing values must be canonical
nonnegative decimal integers. Wrong types, malformed numbers, and increment
overflow leave the value and expiration unchanged.

The counter issues one EVAL and preserves native Redis errors. A failed command
can have an unknown write outcome; the counter never retries. Lua is atomic but
does not roll back earlier commands if a later command fails: permission or
resource errors can leave a repaired expiration even if the increment failed.
Socket cancellation can wait until the context deadline or configured I/O
timeout. Rate-limit decisions and dependency-failure policies belong to the
business.

With the existing `FRAME_TEST_REDIS_*` variables set, run
`go test ./redis -run '^TestWindowCounterIntegration$' -count=1 -v` for
real Lua coverage of concurrent exact increments, fixed expiration and reset,
legacy TTL repair, namespace isolation, precision, malformed values, wrong types,
and overflow. Fixtures use unique keys and clean up only their own keys; legacy
no-expiration fixtures require cleanup by design. The default suite also checks
validation and a lost-reply transport scenario without a Redis service.
