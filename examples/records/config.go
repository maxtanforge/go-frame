package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/maxtanforge/go-frame/app/command"
	appconfig "github.com/maxtanforge/go-frame/app/config"
	"github.com/maxtanforge/go-frame/config"
	"github.com/maxtanforge/go-frame/config/loader"
)

var version = "dev"
var commit = "unknown"
var buildTime = "unknown"

func loadSettings(args []string, lookup func(string) (string, bool)) (config.Result[appconfig.Config], error) {
	defaults := appconfig.Defaults()
	defaults.Service.Name = "records"
	in, err := loader.New(defaults, args, loader.Options[appconfig.Config]{Lookup: lookup,
		AllowDotenv: func(c appconfig.Config) bool { return c.Service.Env == "dev" },
	})
	if err != nil {
		return config.Result[appconfig.Config]{}, err
	}
	result, err := in.Load()
	if err != nil {
		return config.Result[appconfig.Config]{}, err
	}
	if err := validateSettings(result.Value); err != nil {
		return config.Result[appconfig.Config]{}, err
	}
	return result, nil
}

// Inspection reports to errOut; execute remains the sole runtime error owner.
// Signals, the logger, SQL and HTTP are created only inside the run callback.
func executeCommands(ctx context.Context, args []string, out, errOut io.Writer, lookup func(string) (string, bool)) error {
	runtimeOwnsError := false
	err := command.Execute(ctx, args, command.Options{
		Build: command.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime}, Out: out,
		Check: func(_ context.Context, args []string, explain bool) error {
			result, err := loadSettings(args, lookup)
			if err != nil {
				return err
			}
			if explain {
				return json.NewEncoder(out).Encode(result.Entries)
			}
			_, err = fmt.Fprintln(out, "configuration valid")
			return err
		},
		Run: func(ctx context.Context, args []string) error {
			runtimeOwnsError = true
			return execute(ctx, func() (appconfig.Config, error) {
				result, err := loadSettings(args, lookup)
				return result.Value, err
			}, func(ctx context.Context, cfg appconfig.Config, lg *slog.Logger) error {
				ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
				defer stop()
				return runApplication(ctx, cfg, lg)
			})
		},
	})
	if err != nil && !runtimeOwnsError {
		_, _ = fmt.Fprintln(errOut, err)
	}
	return err
}
