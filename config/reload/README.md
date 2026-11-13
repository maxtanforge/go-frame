# Controlled configuration reload

`Manager[T]` publishes versioned snapshots (starting at 1). Supply mandatory
`Clone` and `Validate` callbacks, plus optional `Prepare`. `Clone` must make a
deep-enough independent copy and support concurrent calls. Inputs, returned
snapshots, and callback arguments are copied. Do not mutate a candidate while
calling `Update`.

`Validate` is pure. Optional `Diff(current, candidate) []string` reports the
consumer's own path vocabulary for what changed; `Update` returns the set from
successful updates without interpreting it (nil when absent, nil on any error).
`Prepare(current, candidate)` must be side-effect-free and
may reject static changes with `ErrRestartRequired`. Its optional commit function
must be bounded, cannot fail, and only applies a prevalidated in-memory action.
Callbacks must not panic or reenter the manager. This contract does not provide
rollback for external I/O, or atomic publication across external effects and
the configuration snapshot.

Updates serialize with cancellable acquisition. Validation, preparation,
cancellation before commit, and version exhaustion leave the old revision intact.
After commit, publication completes even if cancellation arrives. An unsuccessful
update returns a detached current snapshot. Callback errors expose a safe stage
description; only `ErrRestartRequired`, `context.Canceled`, and
`context.DeadlineExceeded` are retained for `errors.Is`. A `*config.FieldError`
with a recognized rule is copied and retained for `errors.As`, preserving field,
environment, source and expected type without retaining arbitrary callback
wrappers. These diagnostics must contain schema/source metadata, never input
values; use diagnostics produced by `config.Decode`. Raw errors are discarded.
A zero/nil manager's snapshot is zero; its updates fail safely.

`Watch(ctx, WatchOptions{...})` blocks and cooperates with cancellation. It watches
1–32 explicit regular files, up to 1 MiB each, by SHA-256 content hash. Defaults
are a 500 ms interval and 300 ms debounce; the minimum interval is 10 ms. Duplicate
normalized paths, empty paths, negative timings, and a missing `OnChange` fail.
Symlink targets are followed, including projected-file replacements. Nonregular
targets are rejected before opening; watched paths and parent directories must
be controlled by the application/operator, not an adversary racing special-file
replacements. File operations are not forcibly interrupted.

The first healthy observation invokes `OnChange` once to reconcile changes made
between initial application loading and watcher startup. Consumers should skip
unchanged effective settings. Later callbacks run only after observed content
stays stable for the debounce period. Reads, `OnChange`, and `OnError` are serial;
callbacks must finish promptly and honor cancellation. The watcher reobserves
after callbacks so changes during loading trigger another debounced attempt.
Content changes that occur and revert entirely between observations cannot be
detected by polling.

Missing, oversized, unreadable, or nonregular files are reported through optional
`OnError`, using a file index and fixed reason, never paths or file contents.
Each changed error observation is reported once. Unhealthy observations suppress
reload callbacks; recovery resumes them. Callback errors are sanitized and
reported without terminating the watcher. Later changes retry. Cancellation is
a normal stop returning nil. There are no listeners, background goroutines per
event, filesystem-recursive watches, or automatic changes to process environment.

On Windows, polling handles permit deletion. Some replacement APIs still reject
an open destination and may require editor retries during the brief read window.
This does not change the file-sharing behavior of consumer loaders.

Verification: `go test ./config/reload -count=1` (and `-race` where supported).
