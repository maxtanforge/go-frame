package startup

import (
	"context"
	"errors"
	"log/slog"

	"github.com/maxtanforge/go-frame/telemetry/logger"
)

// RunOptions leaves business configuration and resource composition with the caller.
// Load must validate business settings; Logging extracts settings for
// logging. Fallback defaults to slog.Default, which is never changed by Run.
type RunOptions[T any] struct {
	Load     func() (T, error)
	Logging  func(T) LogSettings
	Run      func(context.Context, T, *slog.Logger) error
	Fallback *slog.Logger
}

// Run owns loading diagnostics and the logger until the application callback has
// returned. Callers use the returned error for exit status, not duplicate logging.
// It opens no application resources or signal handlers. Panics propagate after
// log cleanup; distinct close failures are reported through the fallback sink.
func Run[T any](ctx context.Context, opts RunOptions[T]) (runErr error) {
	fallback := opts.Fallback
	if fallback == nil {
		fallback = slog.Default()
	}
	ready := false
	defer func() {
		if !ready && runErr != nil {
			fallback.Error("application stopped with error", "error", runErr)
		}
	}()
	if ctx == nil || opts.Load == nil || opts.Logging == nil || opts.Run == nil {
		return errors.New("startup: context and callbacks are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	value, err := opts.Load()
	if err != nil {
		return err
	}
	cfg := opts.Logging(value)
	logOptions, err := LoggerOptions(cfg)
	if err != nil {
		return err
	}
	meta, err := cfg.Service.Metadata()
	if err != nil {
		return err
	}
	fallback = slog.New(fallback.Handler().WithAttrs(meta.Attrs()))
	logs, err := logger.Open(logOptions)
	if err != nil {
		return err
	}
	lg := slog.New(logs.Logger().Handler().WithAttrs(meta.Attrs()))
	ready = true
	return runLogged(lg, fallback, logs.Close, func() error { return opts.Run(ctx, value, lg) })
}

func runLogged(lg, fallback *slog.Logger, closeLogs func() error, run func() error) (runErr error) {
	returned := false
	defer func() {
		if runErr != nil {
			lg.Error("application stopped with error", "error", runErr)
		} else if returned {
			lg.Info("application stopped")
		}
		if closeErr := closeLogs(); closeErr != nil {
			fallback.Error("logger stopped with error", "error", closeErr)
			runErr = errors.Join(runErr, closeErr)
		}
	}()
	runErr = run()
	returned = true
	return runErr
}
