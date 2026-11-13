# Startup and logger ownership

`LoggerOptions(LogSettings)` validates service identity and logging settings and maps them into logger options. `Run` also owns the
logging lifecycle around an explicitly supplied application callback.

`Log.Format` defaults to `console`, with readable text output for stdout
and files. Set `LOG_FORMAT=json` for structured JSON, or `LOG_FORMAT=sls` for
SLS JSON fields (`message`, `env`, `level`, `pid`). SLS `env` comes
from `APP_ENV`; `message` contains the same uncolored console-format text.
Format selection is independent of `LOG_OUTPUT=stdout|file|both`.
Console omits automatic source locations; JSON includes them. Direct logger
construction can explicitly enable `Options.AddSource` for either format.
`LOG_TIMEZONE` controls both record timestamps and archive filenames.

Import `github.com/maxtanforge/go-frame/app/startup` as `startup` and
`github.com/maxtanforge/go-frame/app/config` as `appconfig`.

```go
err := startup.Run(ctx, startup.RunOptions[appconfig.Config]{
    Load: appconfig.Load,
    Logging: func(c appconfig.Config) startup.LogSettings {
        return startup.LogSettings{Service: c.Service, Log: c.Log}
    },
    Run: runApplication,
})
// main translates err into an exit status; it does not log the same error again.
```

For business-owned settings, use that type as T and validate them in Load.
Logging extracts only ServiceConfig and LogConfig into LogSettings. Run validates
these two components, opens the logger, adds Service.Metadata() attributes, then
invokes the application. HTTP, database, Redis, lifecycle and ID settings are
outside startup logging. It starts no signal handling, SQL pools, HTTP listeners or global container.
Basic and records demonstrate explicit resource composition in their callbacks.

The logger stays alive until the callback has returned, so application shutdown
and resource cleanup can still write diagnostics. Early failures are logged once
through Fallback (slog.Default when omitted). Application errors are logged once
before close. A distinct close failure goes through the fallback and is joined
with the application error. Supply an independent fallback sink; it must not be
the owned logger being closed. Runtime panic closes the logger and propagates,
without claiming normal completion. No default global logger is changed.

Applications needing direct ownership of the logger runtime, such as configured's
dynamic SetLevel operation, may continue composing LoggerOptions and logger.Open.
They must retain the same single-owner reporting and cleanup responsibility.
