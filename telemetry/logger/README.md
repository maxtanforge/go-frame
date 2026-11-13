# Managed logging

`Open(RuntimeOptions)` validates the format, level and file settings and opens the log
file before returning. Keep the returned `Runtime`, use `Logger()` for records,
and call `Close()` at shutdown. `New(Options)` remains a stdout convenience.

The default format is `console`, for stdout and files alike. It uses the layout `[YYYY-MM-DD HH:mm:ss.SSS] [level] message`, with
lowercase levels and ordered, inspect-style metadata objects:

```text
[2026-10-10 12:00:00.000] [info] service started { service: 'orders' }
[2026-10-10 12:00:00.015] [info] request complete { method: 'GET', path: '/orders', status: 200 }
```

Metadata stays on one line, including long nested objects and arrays. Scalar
newlines are escaped; values are never split or truncated. Console and files use
the same layout, and SLS puts that text in its `message` while retaining one JSON
line per record. JSON formatting is unchanged.

`context` and `service` are ordinary metadata; no PID or module header is added.
Groups stay nested and strings/control characters are escaped. A root string
`stack` is displayed as indented continuation lines, written together with the
header so concurrent records and rotation cannot split a stack block. Nested
stack fields remain ordinary metadata. Messages escape non-printing characters,
including terminal controls and Unicode line separators. Stack blocks preserve
newlines and tabs for indentation while escaping other non-printing characters.
File and redirected stdout output are plain text; terminal stdout colors info
green, warn yellow, error red and debug blue. `NO_COLOR` or `TERM=dumb` disables
colors. Coloring happens at the stdout sink, so files and JSON never contain ANSI
color codes. Terminal detection uses go-isatty for Windows and Unix support.
`Options.Timezone` selects an IANA display timezone; empty preserves the record's
timezone. Service `LOG_TIMEZONE` (UTC by default) controls both record timestamps
and archive filenames. Service configuration enables automatic source locations for JSON; direct
logger construction supports `AddSource` for either format.

Set `Options.Format: "json"` (or service config `LOG_FORMAT=json` / `log.format: json`)
for general newline-delimited structured JSON. The existing
`time`, `level`, `msg`, source, stack and application fields remain structured;
groups are nested JSON objects. One format applies to all enabled sinks.
Deployments parsing JSON should explicitly select `json`.

For the four-field SLS JSON format, use `Options.Format: "sls"` or
`LOG_FORMAT=sls` / YAML `log.format: sls`:

```json
{"message":"[2026-10-10 12:00:00.123] [info] service started { service: 'orders' }","env":"prod","level":"INFO","pid":"1234"}
```

`message` is the same uncolored text as console/file format, including metadata
and stack blocks (escaped inside one JSON line); `level` is uppercase and `pid`
is a decimal string. `Options.Environment` supplies `env`, defaulting to
`production` for direct logger construction. Service configuration supplies
`APP_ENV` verbatim (`dev`, `test`, `prod`). Application fields cannot override
the four SLS fields; they remain in the formatted message. Use `json` when
individual application fields must remain separate structured JSON properties.
SLS format emits the content object for a collector, not the API request envelope.
The shipping agent owns the SLS record timestamp, topic and source. No SLS SDK
or shipping agent is included. One selected format applies to stdout and files;
file output defaults to readable console text and is always uncolored.

`Open` accepts empty (console), console, json or sls, case-insensitively, and rejects
unknown formats or timezones before opening files. `New` falls back to console
for an unknown format, or the record's timezone for an unknown timezone, and warns.
Service configuration accepts `console`, `json` or `sls`.

```go
runtime, err := logger.Open(logger.RuntimeOptions{
    Options: logger.Options{Level: "info", AddSource: true},
    File: &logger.FileOptions{Path: "logs/service.log"},
    Stdout: true,
})
if err != nil { return err }
defer runtime.Close()
runtime.Logger().Info("service started")
```

File defaults are 100 MiB, 10 archives, 7 days, gzip enabled, UTC archive names.
`MaxSizeMB: 0` selects the 100 MiB default.
`MaxBackups`, `MaxAgeDays`, and `Compress` are pointers: nil selects the default;
explicit zero retention means unlimited, and explicit false disables gzip.
Count and age limits both apply: an archive is removed when either limit excludes
it. These settings do not impose a hard disk quota: rotation and compression need
temporary disk space, including space for the original and compressed archive.
Timezone accepts IANA names; timezone data is embedded for portable operation.
Parent directories are created automatically. A single encoded record larger
than the file size limit is rejected without truncating it.
Relative file paths are resolved to absolute paths when opened, so later working
directory changes do not redirect rotation or maintenance.

`SetLevel` accepts debug, info, warn, error, or empty (info), case-insensitively.
Existing child loggers see level changes. `Rotate` archives the current nonempty
file; empty rotations still schedule retention. Unique archive names survive rapid
rotations and restarts. Retention also runs on open and only recognizes archive
names belonging to the configured base file. Each path must have a single owner;
sharing the same file between runtimes or processes is unsupported.

Writes and archive publication are serialized. Each file runtime owns one
maintenance worker with a 16-job queue for gzip and retention. Logging continues
during compression; when the queue fills, rotation waits for capacity while
holding the write lock, applying bounded backpressure. Retention protects queued
archives until their compression attempt finishes, so count limits may temporarily
be exceeded. Opening still performs startup retention synchronously. Close drains
all queued work before returning; slow storage or compression can delay shutdown.
Successful writes are not an
fsync durability promise. After `Close`, records fail and cannot reopen the file;
repeated and concurrent close returns the same result, and stdout is never closed.

All configured sinks are attempted independently. Output and lifecycle failures
(including short writes) increment `ErrorCount`. Since slog discards handler
errors, an `OnError` callback reports them outside handler/writer locks. Reports
are limited to one per second, recursive/concurrent reports are suppressed, and
callback panics are isolated; suppressed failures still increment the counter.
Callbacks should return promptly, be concurrency-safe with their other callers,
and report through an independent output channel. Callback execution is synchronous
and can add latency to the operation reporting the failure.
Without a callback, reports go to stderr. `Rotate` returns archive publication
errors directly; `Close` returns file close errors and the first background failure.
Background failures increment ErrorCount
immediately and the latest unreported failure is reported by the next logging,
Rotate, or Close call. Close also returns the first background failure, even if
already reported. Only the first failure and latest pending report are retained
to bound error storage; individual failures still increment the counter. This
keeps callbacks outside the worker so a callback can safely call Close. Validation
errors are returned directly. Compression failures
preserve the uncompressed archive unless independent count/age retention removes it. Compression is attempted for newly rotated
files only. Opening a runtime runs retention but does not retry compression of
older uncompressed archives, including archives left by a compression failure or
an interrupted process. Gzip enabled therefore does not guarantee that every
existing archive is compressed; recovering those archives requires separate
maintenance.

Gzip intermediates use `.<archive filename>.logger-gzip-<digits>` names. Opening
or maintaining a runtime removes interrupted regular intermediates belonging to
its exact log basename, even when compression is disabled. Cleanup failures are
returned (and counted/reported for runtime operations). Symlinks, directories,
other basenames, and legacy ownerless `.logger-gzip-*` files are left untouched;
legacy intermediates require operator cleanup. This recovery assumes one owner
per path; worker retention and recovery never run concurrently with compression.
The original archive remains available after interrupted compression, subject to retention.
An interruption after publishing the gzip but before removing the source can
leave both copies; they remain separate archives for retention, without automatic
deduplication. Recovery does not promise durability across storage failure.

Archive and intermediate filename patterns are compiled once per owned file
writer. Maintenance retains the same literal-basename ownership rules without
rebuilding matchers on every rotation. The `LogMaintenance` benchmark isolates
this work, including worker coordination. LogRotate waits for completed background
maintenance to measure total rotation and gzip work rather than enqueue latency.

When `StackOnError` adds an attribute, the handler clones the slog record first.
This allows copies of one record to pass through multiple handlers without
modifying shared attribute storage.

Stack capture reuses an 8 KiB scratch buffer through sync.Pool. Each stack
attribute copies its bytes into an owned string before returning the buffer, so
handlers may retain records safely. BenchmarkErrorStack measures the full ERROR
logging path with stack capture.
