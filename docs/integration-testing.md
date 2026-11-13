# Manual integration and deployment validation

Unit tests need no database. Live integration is explicit and uses disposable
test services; no CI workflow is installed.

## Real dependencies

Provide `FRAME_TEST_MYSQL_DSN`, `FRAME_TEST_POSTGRES_DSN`, `FRAME_TEST_REDIS_ADDR` and,
if needed, `FRAME_TEST_REDIS_USERNAME` / `FRAME_TEST_REDIS_PASSWORD` and
`FRAME_TEST_REDIS_DB` (default 0) in your shell or
secret manager. Do not commit their values. MySQL and PostgreSQL users need
CREATE/DROP permissions **in a dedicated test database**.

Run `./scripts/integration.ps1` on Windows, or `sh scripts/integration.sh` on Linux.
These entry points require all three services rather than silently succeeding
with skipped integrations. They include the core adapters, Redis cache and lease
lock adapters, and both Ent dialects. To target one configured service, run its package
with `go test ./database/postgres -run Integration -count=1` (or mysql/redis).

SQL tests create uniquely named `frame_test_` tables, verify committed writes are
visible and rolled-back writes are absent, exercise pagination and millisecond
audit fields, then check cancellation and deadline-bound waiting for a saturated
one-connection pool. They drop only their own tables.
Redis tests use a unique expiring key and remove it during cleanup. A terminated
test process can leave a SQL test table behind; remove such fixtures only in the
dedicated disposable database after confirming no test run remains active.

With only Redis configured, run
`go test ./redis ./cache/redis ./lock/redis -run Integration -count=1 -timeout=2m`.
Cache integration verifies missing/empty/binary values, bounded Lua reads,
expiration and deletion. Lock integration verifies contention, deadline-bound
waiting, renewal beyond the original TTL, release/reacquisition, ownership-safe
deletion and expiry without renewal. These adapters use random namespaces and
delete only their explicit fixture keys; no key scans or database flushes occur.
Both tests close the borrowed client after lease cleanup. This does not validate
Redis failover, fencing, cluster topology or exactly-once business execution.

## Runtime and failure exercises

- On Linux with a supported C toolchain, run `sh scripts/verify.sh --race`.
- Run the records example against each database and verify create/read behavior;
  apply its supplied schema explicitly, before starting the process.
- In a disposable deployment, stop/restart the backend while sending requests.
  Check readiness becomes unavailable, liveness stays healthy, errors are bounded
  by configured timeouts, and successful traffic recovers after the backend returns.
- Hold a long request through SIGTERM. Check readiness withdrawal, drain timeout,
  remaining cleanup budget, telemetry flush, and process exit within the platform
  grace period. Inject disk-full/log-output failure separately on a test volume.
- In the business repository, build/run its service artifact and validate its
  deployment configuration against the [runtime constraints](operations.md). Cross-compilation alone
  does not validate platform behavior, TLS configuration or rolling updates.

These manual exercises require operator-controlled infrastructure; the repository
does not terminate databases or alter a cluster automatically.

## Ent CRUD integration

`examples/ent` additionally provides `TestIntegrationEnt`. Set the SQL
variables above and run `go test ./examples/ent -run '^TestIntegrationEnt$' -count=1 -v`.
Each dialect without a connection variable is skipped. Unlike the core SQL
fixtures, this test uses only session-local temporary tables on a pinned
connection and never writes persistent business tables. See the
[example instructions](../examples/ent/README.md) for connection formats and isolation.
