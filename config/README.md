# Generic configuration

Import `github.com/maxtanforge/go-frame/config` for schema decoding, layered sources and safe diagnostics. This package is independent of deployment components and application startup.

## Business-owned configuration and layers

`Decode[T](defaults, sources)` accepts a struct and returns `Result[T]` with the
decoded `Value` with independent supported fields and field-order `Entries` describing the winning source.
Sources apply in order. Every supplied value is parsed even when a later source
replaces it. Use `Source{Name: "yaml", Values: map[string]string{...}}` for canonical
dot paths, or `Source{Name: "environment", Lookup: os.LookupEnv}` for declared
environment keys. A source cannot use both. Names are short identifier labels,
not file paths. Unknown map keys are errors; lookups only receive declared keys.

`Prepare[T](defaults)` checks the schema and finite defaults and returns a
prepared decoder with captured defaults. It does not invoke business validators,
text decoders or formatters. `prepared.Entries()` returns fresh field metadata
with empty values, suitable for capturing declared environment keys.
`prepared.Decode(sources...)` decodes a fresh copy and validates the final value
once, just like `Decode(defaults, sources...)`. Required business fields may be
empty in defaults and filled by later sources. The optional `config/loader`
uses this API to freeze defaults and environment at construction and validate
only after each complete load. Neither decode entry point can skip validation.

Exported nested structs are traversed. Paths default to lower-case Go field names
; compound fields can carry explicit snake_case tags; `config:"name"` changes a segment, `config:"-"` skips
it. `env:"KEY"` declares an environment key. `envPrefix:"PAY_"` on a nested struct
composes with descendant declarations. Duplicate paths or environment names fail.
`secret:"true"` hides a leaf or whole subtree in provenance. Mark credentials and other sensitive settings secret.

Supported leaves are strings, booleans, finite signed/unsigned numbers, durations,
`encoding.TextUnmarshaler` values, and slices of these scalar types. Lists use JSON
array syntax (`[]`, `[1,2]`, `["a,b","c"]`). Null, nested lists, maps, pointers and
interfaces are unsupported. Limits are 4096 schema paths, depth 32 and 64 KiB per
source value. Field/path segments and source labels use letters, digits, `_` and
`-`, beginning with a letter or `_`; source labels are at most 64 characters.

`Entries` use source `defaults` until overridden. Strings and durations are quoted;
scalar lists use JSON syntax; secrets display `[REDACTED]`. Custom decoded types
show `[custom]` rather than invoking arbitrary formatting methods. Entries never
store raw source values. Root `Validate() error`, on either a value or pointer
receiver, runs after decoding. Ordinary errors are replaced by
`config: validation failed` to avoid disclosing custom validation details.
The optional `app/config.LoadFrom` preset uses this decoder and exposes the same safe field diagnostics.

## Safe field diagnostics

For a structured validation failure, return `config.Invalid("business.message",
config.RuleLength)` from the root validator. For nested configurations use
`config.PrefixValidation("service", c.Service.Validate())`. Nil stays nil.
Component validators use leaf paths; the containing schema supplies their prefix. Validation reports the first failure; it does not collect an error tree.

`Decode` exposes conversion and recognized validation failures as `*config.FieldError`:

```go
var field *config.FieldError
if errors.As(err, &field) {
    // Path, Env, Source, Expected and Rule contain metadata only.
    // Source is the winning layer for validation; the failing layer for parsing.
}
```

Rules are `type`, `size`, `finite`, `required`, `format`, `range`, `choice`,
`relation`, and `length`. These are categories, not a validation expression
language; the consumer still owns exact business limits. Defaults use source
`defaults`. Source names and schema paths must be application-declared metadata,
never values or file paths containing secrets.

Unknown field paths/rules, ordinary errors, and arbitrarily wrapped errors remain
generic. Use `PrefixValidation` for composition instead of `fmt.Errorf` wrappers.
Recognized diagnostics are recreated from the declared schema and carry neither
input values nor the original cause, even for secret fields. Unknown input keys
and parser syntax errors can still be generic. `Error()` text is for operators;
tools should inspect fields and rule codes rather than match message strings.

Optional `config/yaml` and `config/dotenv` adapters parse explicit readers. Compose
layers as defaults, YAML, dotenv, process environment, then explicit CLI values.
Neither adapter discovers files nor mutates the process environment.


Snapshot independence covers supported exposed fields and slices. Excluded fields and
internals of custom text-decoded types may retain caller-owned references;
custom validators must not mutate that shared state.
