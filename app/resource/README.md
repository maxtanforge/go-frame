# Resource composition

`app/resource` owns one lazily opened dependency of any Go type. Applications supply
Open, optional Probe, and required Close callbacks; clients need no particular
methods. Readiness wiring belongs in the composition root:

```go
db, err := resource.New(resource.Options[*database.DB]{
    Name: "database",
    Open: func(ctx context.Context) (*database.DB, error) {
        return mysql.Open(mysql.Options{DSN: cfg.Database.URL})
    },
    Probe: func(ctx context.Context, db *database.DB) error {
        return db.Ping(ctx)
    },
    Close: func(ctx context.Context, db *database.DB) error {
        return db.Close()
    },
})
if err != nil {
    return err
}
checker, err := health.New(health.Options{Checks: []health.Check{
    {Name: db.Name(), Probe: db.Probe},
}})
if err != nil {
    return err
}
// Construct your server with checker as its health handler.
application, err := app.New(service, app.Options{
    Hooks: []app.Hook{db.Hook()},
    OnShutdown: func() { checker.SetReady(false) },
})
if err != nil {
    return err
}
return application.Run(ctx)
```

This snippet imports `database/mysql` as `mysql`; `database/postgres` exposes the
same `Open` shape and returns the same `*database.DB` resource type.

`New` validates Name, Open and Close without calling them. Start opens the
value and runs Probe, when supplied, before publishing Value. Without Probe,
readiness reports lifecycle state only. Probe never opens a dependency and fails
before successful startup or after Stop. Startup errors preserve their causes for
`errors.Is`. App rolls back failed hooks; manual hook users must also call Stop
after failed Start.

A nonnil value returned together with an Open error is retained for cleanup.
Nil and typed-nil values are rejected and never passed to Probe or Close.
For non-nilable value types, every Open return is an acquisition, including zero
values returned alongside an error; Close must handle those partial values. Use
a pointer or another nilable type if the factory must distinguish no acquisition
from a valid zero value. Stop before Start or cancellation before Open never
closes an unacquired value.

Each resource starts at most once. Stop before Start prevents subsequent startup.
Stop invalidates Value, invokes Close at most once, and caches its result for later
Stop calls. Start and Stop serialize. Stop waits for an in-progress Start, so Open
and startup Probe must honor context; Stop does not forcibly cancel them. Close
receives the original Stop context even if already canceled or expired. Cleanup
callbacks decide how to use that budget and must return promptly. A dependency
whose Close has no context support can be adapted as above, but resource cannot
interrupt that call. Lifecycle callbacks must not call Start or Stop on the same
resource. They may inspect Value without deadlocking.

Probe must support concurrent readiness calls and concurrent Close. An in-flight
probe rechecks lifecycle state before reporting success. A value returned by
Value is borrowed, not a lease: it does not extend the dependency's lifetime and
callers must not close it. Drain application work before stopping resources. For
service construction needing Value, register a later startup hook after the
resource hook, and pass a narrow dependency interface to the business service.

This optional composition package imports `app`; it does not import `health`.
`database` and `redis` do not import it. Keep resource handles and lifecycle wiring
in the composition root; business packages own their dependency interfaces.
There is no global container, lookup registry, dependency discovery, retry, or
restart policy.
