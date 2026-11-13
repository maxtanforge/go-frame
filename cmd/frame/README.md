# Frame development CLI

Build with `go build -o frame.exe ./cmd/frame`. This tool supervises local
development; service structure is maintained in the business repository using
[the reference template](../../templates/service/README.md).

## Development supervisor

From the repository root, run `go run ./cmd/frame dev`. The default package is
`./examples/configured`, with application arguments
`run --config examples/configured/config.yaml --watch-config`.

Use `--dir`, `--package`, `--interval` (500ms), `--debounce` (300ms), and
`--stop-timeout` (12s) to configure the supervisor. Application arguments follow
`--`; even an empty `--` disables the sample arguments. Explicit `--package`
also disables sample arguments. Paths are relative to `--dir`.

The supervisor hashes `.go`, `go.mod`, and `go.sum` files recursively, including
test sources, with limits of 10,000 matching files and 16 MiB of source content.
Directory traversal also caps 100,000 total entries and 64 directory levels,
reads entries in batches, and checks cancellation between batches and files.
It skips symlinks and `.git`, `.frame-dev`, `logs`, `bin`, `vendor`, and
`node_modules` directories. YAML/config-only edits do not rebuild the process.
Scan failures retain the current app and retry at the next scan interval.

Builds run serially. Each successful, current build replaces the old process;
failed or stale builds retain it. An app that fails to start or exits waits for
the next source change rather than entering a restart loop. There is no
automatic rollback after a replacement starts. Each supervisor run owns a
unique directory under `.frame-dev`, cleaned on normal shutdown. Abruptly killing
the supervisor can leave that directory behind. Existing run directories are
never cleaned by another supervisor.

On Windows, builds and apps run inside separate kill-on-close Job Objects. A
private stdin-gated helper is assigned to its job before executing the program,
so descendants inherit containment. Restart/shutdown uses forced termination;
Windows graceful drain hooks are not promised. The internal `__frame_child`
entry point is not a user command. On supported Unix systems, process groups
receive SIGINT and then SIGKILL within the stop budget. SIGINT/SIGTERM cancel
the supervisor. Unix SIGKILL of the supervisor cannot run cleanup handlers.
Replacement waits for the Windows job to report zero active processes or the
Unix process group to disappear. A timeout prevents replacement. Unreaped Unix
zombies can conservatively cause a drain timeout; native Unix behavior must be
validated on the target platform.

Child output goes directly to the supplied stdout/stderr (terminal by default).
No shell evaluates user flags or application arguments.
