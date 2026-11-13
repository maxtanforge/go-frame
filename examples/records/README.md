# Transactional records example

Run from the repository root. PostgreSQL is recommended by default; select your
business database through DATABASE_DRIVER and DATABASE_URL. This example supports
PostgreSQL and MySQL. Use a dedicated example database and account. Apply the
matching schema once with your normal operator credentials:

```sh
mysql -h 127.0.0.1 -u example -p example < examples/records/schema/mysql.sql
# Or, with your connection configured in PG* environment variables:
psql -v ON_ERROR_STOP=1 -f examples/records/schema/postgres.sql
```

The schema scripts create only example tables; there are no automatic migrations
or destructive cleanup scripts. Production schema changes belong in your own
reviewed deployment process. The application account needs SELECT/INSERT on the
two tables, not schema-management privileges.

For MySQL (shell syntax; inject credentials through your environment manager):

```sh
export ID_NODE=0 # distinct node per running replica
export DATABASE_DRIVER=mysql
export DATABASE_URL='example:password@tcp(127.0.0.1:3306)/example?charset=utf8mb4&parseTime=true&loc=UTC'
go run ./examples/records
```

For PostgreSQL:

```sh
export ID_NODE=0 # distinct node per running replica
export DATABASE_DRIVER=postgres
export DATABASE_URL='postgres://example:password@127.0.0.1:5432/example?sslmode=disable'
go run ./examples/records
```

The PostgreSQL TLS setting above is for a local disposable server; configure
verified TLS for remote databases. In PowerShell set variables with
`$env:ID_NODE = '0'`, `$env:DATABASE_DRIVER = 'postgres'` and `$env:DATABASE_URL = '...'`.
The HTTP port defaults to 3000; override `HTTP_PORT` as needed.

## Commands and explicit configuration

```sh
go run ./examples/records version
go run ./examples/records config check --config examples/records/config.yaml
go run ./examples/records config explain --config examples/records/config.yaml
go run ./examples/records run --config examples/records/config.yaml --set http.port=3001
```

Supply `DATABASE_URL` and, for multiple instances, a distinct `ID_NODE` before check, explain
or run. No arguments still means run. Version does not read configuration; check
and explain validate configuration without opening SQL, the logger, HTTP listeners
or signal handlers. A check verifies settings, the required database URL and ID node range, not
connectivity or schema existence. Explain reports field provenance and redacts the
database URL and other declared secrets. The runtime retains one owner for error
logging; command inspection errors are written once to standard error.

The optional [`config/loader`](../../config/loader/README.md) adapter applies
`config.Defaults()` with service name `records`, explicit YAML, an explicitly
named development dotenv file, captured declared environment variables, then
repeatable `--set path=value` overrides. A database URL may come from any source;
it is required only after all sources have been applied. Configuration paths are
rooted directly at `service`, `http`, `database`, `log`, and the other core groups.
Use `--env-file path` only when the final `service.env` is `dev`. There is no
implicit file discovery or environment mutation. Records rejects `--watch-config`;
see `examples/configured` for an application that implements hot reload.

```sh
curl -H 'Content-Type: application/json' -d '{"title":"first record"}' http://127.0.0.1:3000/records
# Copy data.id from the response envelope:
curl http://127.0.0.1:3000/records/REPLACE_WITH_ID
# List with a bounded page and stable sorting:
curl 'http://127.0.0.1:3000/records?current=1&size=20&sort=title,-id'
```

Successful operations return the standard 200 response envelope. Invalid
titles or IDs produce 400, missing records produce 404, and unexpected persistence
errors produce a generic 500. POST trims the title and permits 1–200 Unicode
characters without NUL (rejected consistently for both SQL dialects). IDs are
Snowflake int64 values in SQL BIGINT columns, encoded as decimal strings in JSON.
Each POST runs the record insert and its creation audit insert in one transaction;
either both commit or both roll back. A connection loss during commit can leave
the outcome uncertain, so this example does not automatically retry writes.

`GET /records` defaults to `current=1` and `size=20` only when those parameters
are absent. The service allows sizes 1–100 and offsets up to 1,000,000. Positive
decimal integers are required; signs, empty values, overflow, repeated or unknown
parameters, and malformed URL escapes return 400. Query strings over 4096 bytes
return 413.
`sort` accepts comma-separated `id` and `title`, with `-` for descending order.
Empty, unknown, and repeated fields are rejected (at most eight terms). The store
appends `id ASC` when the request has no ID sort, so equal titles have a unique
ordering; an explicitly requested ID direction is preserved.

The response `data` uses `pagination.Slice`: `current`, `size`, `has_more`,
and `records`. It fetches at most
`size+1` rows, removes the probe row, and derives `has_more` without a COUNT query.
Empty `records` is `[]`. Values use bound MySQL/PostgreSQL parameters, and sort
columns come from a static whitelist. Offset pagination is deterministic for an
unchanged dataset; concurrent inserts or deletes can shift subsequent pages.
Deep offsets still require database work despite the bound; choose a cursor
design separately if the application needs it.

`internal/record` owns narrow repository and transaction interfaces. HTTP calls
the service, which selects its atomic work; `internal/sqlstore` implements those
interfaces using explicit MySQL or PostgreSQL placeholders and `database.WithTx`.
The database resource opens and probes during startup, then a later hook builds
the store, service, and routes before HTTP starts. Shutdown drains HTTP before
closing the pool, using configured startup, shutdown and cleanup reserve budgets.
The metadata-enriched logger stays open until cleanup finishes.

```sh
go test ./examples/records/...
```

Tests cover service validation and cancellation, transactional failure, strict
HTTP list parsing with request recorders, probe-row metadata, and both SQL dialects
with a deterministic SQL driver. SQL tests verify bind arguments, unique ordering
for duplicate titles, no count query, iteration/scan/close failures, context
propagation, and bounded reads. They do not require a live database or
claim to verify a deployed server. Manual database commands above require a
server and credentials supplied by the operator. This example has no
authentication; add application-specific authorization before exposing it.

Records include `created_at`, `updated_at`, `created_by`, and `updated_by`.
Create, detail and list HTTP responses encode both timestamps as Unix millisecond
JSON integers (for example `1700000000123`), while IDs remain decimal strings.
The HTTP layer uses a shared response DTO with `respond.MillisTime`; domain and
SQL values remain `time.Time`. Do not serialize the domain record directly as an API response.
The service samples its injectable `Now` clock once inside the transaction and
stores UTC timestamps at millisecond precision. The creation event uses exactly
the same actor and timestamp as the record. Reads validate stored audit fields.
The MySQL example requires `parseTime=true&loc=UTC` (UTC is also the driver's
default location); invalid DSN settings fail before opening the database pool.
The example enforces `timeTruncate=1ms` when preparing the MySQL DSN, overriding
any caller setting so driver encoding preserves the audit timestamp precision.
The schemas use MySQL `DATETIME(3)` and PostgreSQL `TIMESTAMPTZ(3)`.

`Register` receives the fixed example actor `system/records-example`. Request
headers such as `X-User-ID` never supply identity, and extra JSON fields such as
`actor` are rejected. Applications should obtain actors from their own trusted
entry points and pass them to `Service.Create`; this example provides no
authentication or authorization. The audit table records creation only, with one
row per record; it is not a complete change history. Existing deployments must
apply an explicit migration before using these added non-null columns.

ID generation is injected and one generator is shared within an application. The default
node is 0 for local development; the sample YAML uses the same default.
See [node allocation and restart constraints](../../id/README.md) before deploying
multiple replicas. No node allocation service or clock persistence is provided.
