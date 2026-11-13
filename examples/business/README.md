# Category business contract example

This example includes a runnable category CRUD service and focused contract tests.
Start with [running the service](RUNNING.md): `cmd/api` and `internal/bootstrap`
assemble authentication, operation recording, an Ent store and resource lifecycle.
PostgreSQL is recommended; the application selects its driver and URL explicitly.
The feature lives under `internal/category`, with `http` and `store` adapters.
For a minimal new service, start from the [independent service template](../../templates/service/README.md).
To learn startup wiring without a database, read [basic](../basic/main.go).
This example is the complete business reference; choose additional capabilities through
the [example navigation](../README.md).

Two focused integration examples supplement the category service (added in
v0.2.2; not mounted by `cmd/api`):

- [Profile updates](internal/profile/README.md): actor/target authorization,
  nested PATCH presence, a typed downstream permission client and atomic updates.
- [Device synchronization](internal/devicesync/README.md): durable acceptance,
  replayable delivery, bounded scheduling and lifecycle-owned shutdown.

Both are included in root tests and the external business consumer fixture.
Their stores and upstream contracts belong to the example business, not the
framework API; use their tests to adapt the relevant behavior in your service.

[`category`](internal/category) owns values, models, commands and use cases;
[`categoryhttp`](internal/category/http) owns transport DTOs and explicit response/error
mapping; [`store`](internal/category/store) implements persistence with Ent.
The business package imports only the standard
library. It has no HTTP, optional-field, ORM or framework validation dependency.

| File | Responsibility |
| --- | --- |
| [internal/category/id.go](internal/category/id.go) | ID value object and canonical parser |
| [internal/category/model.go](internal/category/model.go) | business snapshot and update commands |
| [internal/category/service.go](internal/category/service.go) | rules, permissions and repository capability |
| [internal/category/catalog.go](internal/category/catalog.go) | creation, counted listing and deletion use cases |
| [internal/category/http/dto_request.go](internal/category/http/dto_request.go) | request DTOs, syntax validation and PATCH presence mapping |
| [internal/category/http/vo_response.go](internal/category/http/vo_response.go) | CategoryVO/DeleteVO response view objects and field whitelist |
| [internal/category/http/handler.go](internal/category/http/handler.go) | named Handler methods adapting business use cases |
| [internal/category/http/create_handler.go](internal/category/http/create_handler.go) | prepared creation handler and parent-to-category audit ordering |
| [internal/category/http/routes.go](internal/category/http/routes.go) | protected route declarations and atomic five-route collection |
| [internal/category/http/errors.go](internal/category/http/errors.go) | safe public error mapping |
| [internal/category/store/store.go](internal/category/store/store.go) | native Ent transactions and locked authorization |
| [internal/bootstrap](internal/bootstrap) | settings, verifier, pool ownership and HTTP startup |

Request DTOs, response view objects (VO), named business-adaptation handlers,
and route/error declarations stay in the same feature HTTP adapter. A VO selects
public response fields; domain value objects such as category.ID encapsulate rules.
There are no global entity/VO/DTO directories.

`category.ID` is a positive int64 with a private value. `ParseID` accepts only
canonical decimal strings: `1` through `9223372036854775807`. It rejects `01`,
`+1`, zero, negatives, whitespace and overflow. `NewID` accepts numeric values
from a persistence adapter; `Int64` exposes a numeric value for that adapter.
The zero value remains invalid: constructors cannot stop a caller from using
`ID{}`, so every use case checks `Valid` again.

`Model` has no transport or ORM tags. `PatchCommand` selects only name and note
changes. Category ID, parent ID and owner are immutable through this use case;
the HTTP DTO whitelist excludes them and the response whitelist exposes only
ID, name and note. System categories cannot be edited, even by their owner.
Names must contain non-whitespace text and fit 100 UTF-8 bytes; notes fit 1000
UTF-8 bytes and may be empty. Both reject invalid UTF-8 and NUL. These are business
rules, checked by the service for HTTP, jobs and other non-HTTP callers.

The HTTP path DTO uses custom validation for canonical ID syntax, then maps to
the value object. The PATCH DTO uses `optional.Field` and maps presence explicitly:

| JSON note property | Command | Result |
| --- | --- | --- |
| omitted | zero `NoteChange` | retain stored note |
| `null` | `Clear: true` | remove note |
| `""` | `Set: true, Value: ""` | store an empty note |
| string | `Set: true, Value: text` | store text |

Name omission retains the stored name; name null is rejected by binding.
Conflicting `Set` and `Clear`, empty patches and invalid text are rejected by the
business service even when the command did not come from HTTP. `Note` plus
`HasNote` in the model preserves empty versus absent without retaining pointers.

A non-HTTP caller can issue a command directly using its trusted actor identity:

```go
id, err := category.ParseID("42")
if err != nil {
    return err
}
model, err := service.Patch(ctx, trustedActorID, category.PatchCommand{
    ID: id,
    Note: category.NoteChange{Clear: true},
})
```

The actor identity is supplied by trusted application code, not accepted from
request JSON. HTTP reads the validated principal from the request context. The
service independently checks that the actor is present and owns the loaded
category. `Repository.Update` must keep load, ownership/state checks and write in
one atomic operation; separate `Find` then `Save` would allow a race. Callback
errors and observed cancellation discard mutations. Context is forwarded
unchanged, and returned models are independent value snapshots. Neither caller
nor repository may retain callback pointers.

The mutex-backed repository in `category_test.go` is only a contract fixture:
it copies before invoking the callback, commits only on success and cancellation
checks, and tests rejection rollback plus absence of retained store pointers.
The HTTP capture repository is only a mapping fixture. Neither is a production
repository. The runnable service uses the [Ent store](internal/category/store/store.go),
whose mutations lock and authorize inside a transaction. See [running the service](RUNNING.md)
for explicit migrations and optional PostgreSQL integration verification.

`categoryhttp.Routes(service)` uses `binding.Path`, `binding.JSONWithPath` and
`Route.Bind`/`router.Collect` to construct GET and PATCH `/categories/{id}` together.
Both declare operation metadata and retain the default protected policy. Pass the
returned routes to `router.NewAuthenticated` with the application's verifier.
Mounting their handlers on plain chi does not enforce that declared policy.
Callbacks map validated DTOs directly; they do not repeat generic validation.
GET and PATCH configure the module's `httpError` through `binding.Options.MapError`;
their callbacks return business errors directly. Only callback failures use this
mapper; decoding and validation failures keep the framework's input responses.

The test-only `TypedRoutes(service)` helper compares experimental typed composition
helpers with the same production Handler methods, options and policies. Production files
do not import the experiment. Authenticated parity tests
cover detail and PATCH contracts; the [entry comparison](../contracts/README.md)
also includes an independent transaction pagination consumer. These helpers are
not a second public registration API.

`categoryhttp.CreateHandler(create)` demonstrates a separate POST
`/books/{bookId}/categories` with a strict audit sequence. Startup prepares both
DTO decoders and validation plans. Each request validates the canonical parent ID,
sets the operation resource to `book` and its ID, decodes and validates JSON,
calls the injected business use case, then sets the resource to the returned
`category` ID and sends the same public response whitelist. An invalid path reads
no body and records no resource identity. Body or business failure retains the
parent; observed cancellation stops subsequent stages and retains simultaneous
business failures for diagnostics. A missing active recorder (including metadata
mounted without recorder middleware) or resource-setting failure is returned
explicitly. An invalid returned category ID
produces a safe 500 and retains the parent.

The injected `CreateUseCase` receives the context, trusted actor, parent value
object and name. It owns permissions, name invariants, persistence and transaction
composition, and must return a valid new ID on success. The DTO checks name
presence and type; the use case checks meaningful text and business limits. This
example supplies no SQL creation repository. The low-level `respond.Adapter`
handler explicitly calls `httpError` only for business failures. It does not use
`JSONWithPath`, whose combined source validation does not provide this sequence.

Compose the prepared handler as a protected declaration separately from GET and
PATCH (inside a startup function returning `([]router.Route, error)`):

```go
handler, err := categoryhttp.CreateHandler(createUseCase)
if err != nil {
    return nil, err
}
return router.Collect("categories",
    router.Route{
        Method: http.MethodPost,
        Path: "/books/{bookId}/categories",
        Operation: operationhttp.Metadata{Action: "category.create"},
    }.Bind(handler, nil),
)
```

Pass the declaration to `router.NewAuthenticated` with the application's verifier
and operation recorder. Operation events describe HTTP execution; they are not
proof of a database transaction commit. This ordering remains local to this use
case rather than introducing a general pipeline API.

Domain errors are matched with `errors.Is` and translated to safe public
status/code/message values. Ownership denial maps to 403, immutable state to 409,
and missing categories to 404. Unknown store errors map to a generic 500. The
original cause remains in `httperror.Error.Err` for internal handling, never in
the response message. Cancellation and deadlines retain their original errors.

The mapping belongs to this example's HTTP adapter; these codes are application
choices, not a global Frame error catalog:

| Business error | HTTP status | Public code | Public message |
| --- | --- | --- | --- |
| `ErrInvalid` | 400 | 40001 | invalid category input |
| `ErrUnauthenticated` | 401 | 40101 | authentication required |
| `ErrForbidden` | 403 | 40301 | category access denied |
| `ErrNotFound` | 404 | 40401 | category not found |
| `ErrImmutable` | 409 | 40901 | category cannot be modified |
| unknown store cause | 500 | 50001 | internal server error |

The adapter retains each mapped cause. Native context errors bypass this table
and keep Frame's existing cancellation/deadline response behavior.

Run the focused tests from the repository root:

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'frame-doc-go-cache'
go test ./examples/business/...
```

Tests emphasize business invariants, canonical IDs, atomic rollback, PATCH
presence mapping, permissions, safe errors and parent resource retention/replacement
through a real operation recorder and sink. Frame's own binding tests cover
the broader decoding and validation matrix.
