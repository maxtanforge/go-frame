# Redis leases

`lock/redis` borrows a go-redis client and never closes it. Acquisition uses
`SET <namespace>:<key> <random owner> NX PX <ttl milliseconds>`. Renewal and
release use Lua to compare the owner before changing the key. Use a namespace
reserved for leases, separate from cache/application data.

```go
import (
    "context"
    "errors"
    "time"

    lease "github.com/maxtanforge/go-frame/lock/redis"
    driver "github.com/redis/go-redis/v9"
)

func run(ctx context.Context) (err error) {
    client := driver.NewClient(&driver.Options{
        Addr: "localhost:6379",
        ContextTimeoutEnabled: true,
        DialTimeout: time.Second,
        ReadTimeout: time.Second,
        WriteTimeout: time.Second,
        MaxRetries: -1,
    })
    defer client.Close() // Application owns the client.
    locks, err := lease.New(client, lease.Config{
        Namespace: "billing:leases",
        TTLMS: 15000,
        CommandTimeoutMS: 1000,
        PollIntervalMS: 100,
        RenewIntervalMS: 5000,
    })
    if err != nil { return err }
    held, err := locks.Acquire(ctx, "daily-export", false)
    if errors.Is(err, lease.ErrContended) { return nil } // Skip this run.
    if err != nil { return err }
    // Register cleanup before business work, including panic paths.
    defer func() {
        cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
        defer cancel()
        err = errors.Join(err, held.Release(cleanup))
    }()
    workErr := exportInvoices(held.Context()) // Business function honors cancellation.
    return errors.Join(workErr, held.Err())
}
```

All configuration durations are integer milliseconds and are checked for
overflow. TTL, command timeout and poll interval must be positive. Renewal is
disabled when `RenewIntervalMS` is zero; otherwise it must be positive and less
than TTL. Namespace and key must each contain 1–512 bytes without surrounding
whitespace.

`Acquire(ctx, key, false)` returns `ErrContended` immediately when occupied.
With `wait=true`, acquisition polls until successful or canceled; provide a
context deadline to bound the entire wait. Each Redis operation has its own
deadline even when the caller has none. The client must honor deadlines:
enable go-redis `ContextTimeoutEnabled` and configure finite connection/socket
timeouts and retries. A custom client that ignores context violates the client
contract and can block cleanup.

Use the lease's `Context()` for work. Parent cancellation, local expiry or any
renewal failure cancels it. `Err()` and `context.Cause(held.Context())` expose
`ErrLost` for lost/expired ownership, the parent's cause for cancellation, or
`ErrReleased` for explicit release. The local TTL begins before sending a
successful acquisition/renewal, conservatively including network delay. A
separate expiry watcher remains active during renewal commands.

Always release after work finishes. Release cancels work and starts one cleanup
goroutine that joins the lease's background goroutines before owner-checked
deletion. Each caller's context bounds its own wait, including concurrent calls.
Deletion preserves the first caller's context values but uses a fresh command
timeout independent of caller cancellation, starting after the join. In-flight Redis socket I/O may
finish only at its original command deadline, so cleanup can outlive a caller's
wait. Later calls can await the same cleanup result. There is at most one cleanup
goroutine per released lease, bounded by the existing renewal deadline plus the
deletion command budget for a deadline-honoring client. Release does not join
business goroutines.
If renewal already failed, a successful release does not clear `Err()`.

Network errors and unexpected responses never establish ownership. An unknown
acquisition/release outcome can leave a key until TTL expiry. Renewal errors
cancel work immediately even if Redis might have renewed the key. Work must
cooperate with cancellation; no library can stop a paused process or undo a
business side effect. Redis failover and process pauses can allow concurrent
workers. This is neither an exactly-once guarantee nor a fencing-token service.
Protect durable writes with business constraints, idempotency or an independent
fencing scheme. Using a lease around a schedule also does not deduplicate an
entire schedule period.

Tests use a controlled fake client and do not connect to Redis. Real Redis
failover, transport behavior and operational settings require separate
integration acceptance.
