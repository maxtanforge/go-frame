// Package bootstrap is the composition root: it owns configuration, resources,
// logging, signals, HTTP wiring and the application's lifetime.
package bootstrap

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
	"github.com/maxtanforge/go-frame/app/startup"
)

// Execute keeps inspection commands outside the runtime and never exits.
// Run failures are logged by startup.Run; callers use errors for exit status.
func Execute(ctx context.Context, args []string, out io.Writer, lookup func(string) (string, bool), build command.BuildInfo) error {
	return command.Execute(ctx, args, command.Options{Build: build, Out: out,
		Check: func(_ context.Context, args []string, explain bool) error {
			loaded, err := loadSettings(args, lookup)
			if err != nil {
				return err
			}
			if explain {
				return json.NewEncoder(out).Encode(loaded.Entries)
			}
			_, err = fmt.Fprintln(out, "configuration valid")
			return err
		},
		Run: func(ctx context.Context, args []string) error {
			return startup.Run(ctx, startup.RunOptions[settings]{
				Load: func() (settings, error) { loaded, err := loadSettings(args, lookup); return loaded.Value, err },
				Logging: func(s settings) startup.LogSettings {
					return startup.LogSettings{Service: s.Framework.Service, Log: s.Framework.Log}
				},
				Run: func(ctx context.Context, s settings, lg *slog.Logger) error {
					ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
					defer stop()
					return runApplication(ctx, s, lg)
				},
			})
		},
	})
}
