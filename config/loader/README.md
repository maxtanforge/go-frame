# Explicit configuration loader

`config/loader` is an optional adapter over `config`, `config/yaml` and
`config/dotenv`. Neither the core `config` package nor `app/command` imports it.
The application owns its settings type and validation rules.

```go
defaults := config.Defaults()
inputs, err := loader.New(defaults, args, loader.Options[config.Config]{
    Lookup: os.LookupEnv,
    AllowDotenv: func(c config.Config) bool { return c.Service.Env == "dev" },
})
if err != nil { return err }
result, err := inputs.Load()
```

The supported arguments are `--config path`, `--env-file path`, and repeatable
`--set canonical.path=value`. Duplicate override paths, unknown flags and
positional arguments are rejected. Only explicitly named files are read; no
working-directory search, environment expansion or process-environment mutation
occurs. Errors exclude supplied values and file paths. `result.Entries` supplies
provenance with fields marked `secret:"true"` redacted by the core decoder.

Sources apply in this order: defaults, YAML, dotenv, captured declared environment,
then CLI overrides. Supplied invalid values are errors even when a later source
would replace them. An explicit empty value remains an override. The lookup is
required and is called only for environment names declared by the settings schema.

`New` parses arguments, resolves explicit paths to absolute paths, checks the
schema, copies defaults, and captures environment values. It does not invoke
business validation or custom text methods. Required values such as a database
URL may be empty in defaults and supplied by files, environment or CLI overrides.
Each `Load` calls the root `Validate` method once after decoding all sources;
missing requirements fail there with the core decoder's safe diagnostics.
Ordinary slices are copied; as with `config.Decode`, custom text type internals and excluded fields
remain caller-owned and must stay immutable.

Each `Load` rereads named files, decodes a fresh value and validates the final
configuration. Defaults, environment and overrides stay frozen across reloads;
mutating a returned ordinary slice does not change future loads. `Paths` returns
a fresh slice. `AllowDotenv` must explicitly approve development use based on the
final decoded value. Omitting this callback rejects `--env-file`.

`--watch-config` is rejected unless `AllowWatch: true` is supplied. When enabled,
it requires at least one explicit file and `Watch()` reports the requested mode.
The adapter itself starts no watcher, resources or goroutines. Consumers own
watching, dynamic-field allowlists, immutable snapshots and rejection policy;
`examples/configured` demonstrates these with `config/reload`. Recreating inputs
is necessary to capture changed environment or command-line values.
