# Profile integration example

This focused example composes a protected profile PATCH, a typed upstream
permission check and a PostgreSQL transaction. It was added in v0.2.2
and is not an endpoint mounted by the category demo application.

`profile` imports only the standard library. It owns the command, profile value
snapshot, validation, authorization and repository contract. `http` owns request
presence, public response fields and error mapping; `upstream` owns the permission
protocol; `store` owns SQL and transaction details. Copy these packages into your
service's feature directory and adapt the business policy there.

Startup constructs the dependencies explicitly (imports use this directory's
`profile`, `http` as `profilehttp`, `store`, and `upstream` packages):

```go
repository, err := store.New(db) // *database.DB; owned by application lifecycle
if err != nil {
    return err
}
permissions, err := upstream.New(permissionBaseURL, outboundClient, credentials, observe)
if err != nil {
    return err
}
service := profile.Service{Repository: repository, Authorizer: permissions}
routes, err := profilehttp.Routes(service)
if err != nil {
    return err
}
handler, err := router.NewAuthenticated(logger, router.AuthenticatedOptions{
    Verifier: verifier,
    Routes: routes,
})
```

Here `outboundClient` is an `httpclient.Doer`, typically a lifecycle-owned
`*httpclient.Client` configured with timeouts, TLS, pooling and redirect policy.
`credentials` is `func(context.Context) (string, error)` and returns a raw bearer
token for each request. `observe` is an optional concurrency-safe
`func(upstream.Event)`. The constructor validates a trusted HTTP(S) base URL and
adds the fixed `permissions/profile/edit` path to its prefix. User input cannot
select an upstream URL or path. The client performs one call and has no retries.

`PATCH /profiles/{id}` takes its actor only from the authenticated principal.
`Service.Patch(ctx, actorID, targetID, command)` calls `CanEdit` for both self and
explicit-target edits. The permission service accepts
`{"actor_id":"alice","target_id":"bob"}` and must return an explicit
`{"success":true,"data":{"allowed":true}}` or a false `allowed` decision.
Missing success/data/allowed fields, malformed JSON, transport/HTTP failures and
`success:false` are dependency errors, distinct from a valid denied decision.
Authorization reflects the upstream decision at call time; it is not a
distributed transaction with permission changes.

| PATCH body | Effect |
| --- | --- |
| `{"basic":{"display_name":""}}` | Set display name to empty; keep age and note |
| `{"basic":{"age":0}}` | Set age to zero; keep display name and note |
| `{"note":""}` | Store an empty, present note |
| `{"note":null}` | Clear note to SQL NULL |
| `{}` or `{"basic":{}}` | Reject an empty change |

Unknown fields and null basic/name/age are rejected. The service repeats domain
validation for non-HTTP callers: age is 0–150; display name and note are valid
UTF-8 without NUL, limited to 100 and 1000 bytes. Conflicting note set/clear flags
are invalid. An explicitly supplied value equal to its stored value is accepted;
only a command without changes is rejected.

The HTTP adapter maps invalid input to 400, absent identity to 401, denial to 403,
missing profiles to 404 and permission dependency errors to a safe 502. Child
dependency cancellation/deadlines retain their cause and map to 502 while the
incoming request is live. `respond` owns the incoming request's cancellation or
deadline. Public errors exclude credentials, upstream bodies and URLs.

Apply [store/schema.sql](store/schema.sql) explicitly through your migration
tooling; the adapter does not create tables. `SELECT ... FOR UPDATE`, the profile
`UPDATE` and the `profile_changes` snapshot insert all use the same `sql.Tx` under
`database.DB.WithTx`. Callback rejection, either write failure or cancellation
rolls back; commit errors return no success snapshot. Change records are profile
snapshot history, not a complete security audit log.

Profile PATCH has no request-idempotency key or deduplication. Repeating an
accepted patch creates another change-history record even when its values are
unchanged. A commit error can leave the outcome unknown to the caller; reconcile
the stored profile and change history before deciding whether to submit again.

The typed client's observer measures the whole call, including credentials,
response reads, decoding and envelope validation. Its labels are the fixed
`profile.permission` operation and bounded outcomes such as `allowed`, `denied`,
`rejected`, `protocol`, `http` and `timeout`. It includes no actor, target, URL,
token or raw body. `httpclient.Options.OnRequest` instead observes each HTTP
exchange through response headers; it cannot classify a later decode or business
failure. `router.Options.OnRequest` describes the incoming HTTP request. Keep
these metrics separate and use only fixed outcome labels for the dependency.

Run `go test ./examples/business/internal/profile/... -count=1` from the repository
root. Tests cover the authenticated route with the real typed client and a fake
Doer, nested PATCH semantics, concurrent per-request credentials, observer
classification and sqlmock transaction failure paths. SQL tests use
`database.Open` with a borrowed connector. They do not replace PostgreSQL
integration testing against your deployed schema and migrations.
