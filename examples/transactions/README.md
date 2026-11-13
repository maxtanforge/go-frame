# Explicit business transactions

This package demonstrates a business service depending on its own narrow `Transactions` and `ProductStore` interfaces. Startup supplies a `SQLTransactions` adapter around its owned SQL pool; repositories borrow `database.Executor`. The product insert and event insert share one SQL transaction. The HTTP adapter maps tagged DTOs to plain business commands and maps business errors at the transport boundary.

The example separates responsibilities by file:

- `service.go`: business commands, capability interfaces and service rules.
- `http.go`: transport DTOs, typed handlers and explicit response mapping.
- `routes.go`: explicit route declarations with authentication and operation policy.
- `meta.go`: an HTTP startup scanning prototype for comparison, not a framework API.
- `sql_repository.go`: SQL data access through the borrowed executor.
- `service_test.go`: transaction and HTTP integration checks without remote resources.

```go
service := transactionexample.Service{
    Transactions: transactionexample.SQLTransactions{DB: db},
    Store: transactionexample.SQLRepository{},
}
routes, err := transactionexample.Routes(service, listProducts)
if err != nil { return err }
httpRouter, err := router.NewAuthenticated(log, router.AuthenticatedOptions{
    Routes: routes,
    Verifier: verifier,
})
if err != nil { return err }
// Supply httpRouter to the startup-owned HTTP server.
```

`db` is an existing startup-owned resource. The example does not connect to remote resources, migrate schemas or create tables. SQL uses MySQL placeholders; adapt queries to the application's dialect. Product and event tables are application schema, not framework models.

For listing, `ListHandler` accepts a business function and a router path extractor. It uses `binding.PathWithQuery` to map a category path and pagination query to `ListQuery`. `Routes` supplies chi extraction and collects construction errors once. Both endpoints remain protected by default; configure `verifier` using the application's authentication implementation. Binding validates DTOs once; `Service.Create` independently protects its business invariants for non-HTTP callers.

The declaration keeps business policy visible:

```go
return router.Collect("products",
    router.Route{
        Method: "POST", Path: "/products",
        Operation: operationhttp.Metadata{Action: "product.create"},
    }.Bind(CreateHandler(service)),
)
```

`CreateHandler` returns `(http.Handler, error)`, so its result feeds directly into `Bind`. A bad schema stops startup and retains its cause alongside the module, method and path. The business application tests its rules, DTO mappings and selected authorization policies; generic decoding and route mechanism tests belong to Frame.

`CreateHandler` supplies its `productError` function through `binding.Options.MapError`.
Its callback returns the business error directly; binding applies the module mapper
only to callback failures and keeps the original cause. Input validation errors and
context cancellation do not pass through the mapper.

Keep these files within a business feature such as `internal/product/`; avoid global `dto`, `vo` and `entity` directories. `createDTO` and `productResponse` describe HTTP contracts. `CreateCommand`, `ListQuery` and `Product` describe business inputs and outputs without transport or persistence tags. When a domain requires money, a canonical identifier or a state transition, add a value type with a constructor and business invariants in that feature. Do not create an empty VO layer solely to rename DTOs. Persistence records or generated Ent entities stay with the store and are mapped at its boundary. Larger features can split commands and models into separate files without changing package dependencies.

`MetaRoutes` declares the same two endpoints using factory fields with tags. It scans only during startup and builds the same typed handlers; no reflection runs while handling requests. Tests compare policy declarations and execute both paths. The prototype requires factory closures, tag parsing and signature checks, so it does not presently reduce complexity enough to replace `Routes`. It does not scan arbitrary methods, expose unannotated dependencies, or define a gRPC entry point. Keep the explicit declaration as the primary example until more business cases justify a broader Meta contract.

Existing code can continue passing `db.SQLDB()` to query repositories or Ent's SQL driver. Existing Ent clients keep their native `client.Tx(ctx)` / `tx.Commit()` / `tx.Rollback()` APIs; an Ent transaction must explicitly pass its transaction client to Ent repositories. The SQL executor callback is intended for standard SQL repositories, not as an adapter for Ent transactions.

Native Redis integration stays explicit:

```go
// cachePool is explicitly injected by startup.
cacheClient := cachePool.Client
value, err := cacheClient.Get(ctx, cacheKey).Result()
```

A SQL transaction does not make Redis updates atomic. The application decides when and how to refresh cache data.

For fixed-window counting, startup can inject a prepared counter into the business capability that needs it:

```go
counter, err := redis.NewWindowCounter(cachePool.Client, redis.CounterOptions{
    Namespace: "ledger.write-quota.v1",
})
if err != nil { return err }
count, err := counter.Increment(ctx, actorKey, time.Minute)
if err != nil { return err } // The business decides its dependency-failure policy.
if count.Count > limit {
    return ErrQuotaExceeded // Application-defined error; no framework quota decision.
}
```

Here `redis` is `redis`, and `cachePool` is an existing startup-owned pool created with retries disabled. The counter borrows its native single-node client. Use a dedicated namespace and never write unrelated values into it. New keys receive expiration atomically; subsequent increments preserve the original window, and valid old counters missing TTL receive an expiration. A lost response has an unknown outcome: do not automatically repeat an increment. An SQL transaction cannot roll it back. Cluster/Sentinel failover and cross-resource transactions are outside this helper's contract.

Run `go test ./examples/transactions`. Tests check that successful commands commit both writes, a failed event insert rolls back, invalid business commands begin no transaction, and typed HTTP adapters map DTOs and responses correctly. Startup closes all borrowed resources; frame has no Close method.
