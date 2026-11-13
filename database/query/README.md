# SQL pagination suffixes

`query` depends only on the standard library and `pagination`. It does not own a
database connection, execute SQL, rewrite existing SQL, or generate count queries.
The storage adapter composes a base query and these suffixes, binds arguments, and
checks `Rows.Err` after reading records.

`OrderBy` accepts public sort aliases and a caller-owned column whitelist. All
mapping values must be dot-separated ASCII identifiers (`[A-Za-z_][A-Za-z0-9_]*`
per segment); raw expressions, quoting, whitespace, and wildcard columns are
rejected. Each identifier segment is quoted for MySQL or PostgreSQL. Both sort
lists reject unknown aliases and duplicates. A nonempty tie-breaker tuple is
required, and the caller must ensure it uniquely orders the actual result set.
Requested fields come first; requested tie-breakers retain their direction. The
combined order has at most eight terms. Deterministic ordering does not prevent
offset pagination from moving under concurrent inserts or deletes.

`LimitOffset` accepts an explicitly constructed, validated `pagination.Window`.
It never fills defaults. The parameter start must be 1 through 65534 for both
dialects. Arguments are always `int64` limit followed by offset. PostgreSQL uses
`$n` and `$n+1`; MySQL uses `?`. For unknown totals, `fetchExtra=true` requests one
extra row so `pagination.NewSlice` can infer whether another page exists.

Both functions return suffixes with a leading space and report `ErrInvalid`
without reflecting invalid input. Callers remain responsible for composing SQL
in the proper clause order and accounting for preceding parameter arguments.
