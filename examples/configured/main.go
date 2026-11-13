// Configured demonstrates business-owned settings and resource-free commands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/app/command"
	"github.com/maxtanforge/go-frame/app/startup"
	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/http/server"
	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

var version = "dev"
var commit = "unknown"
var buildTime = "unknown"

func main() {
	// Only run installs signal handling; version/check/explain need no runtime.
	if err := execute(context.Background(), os.Args[1:], os.Stdout, os.LookupEnv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, out io.Writer, lookup func(string) (string, bool)) error {
	return command.Execute(ctx, args, command.Options{Build: command.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime}, Out: out,
		Check: func(_ context.Context, args []string, explain bool) error {
			r, err := loadSettings(args, lookup)
			if err != nil {
				return err
			}
			if explain {
				return json.NewEncoder(out).Encode(r.Entries)
			}
			_, err = fmt.Fprintln(out, "configuration valid")
			return err
		},
		Run: func(ctx context.Context, args []string) error {
			in, err := newInputs(args, lookup)
			if err != nil {
				return err
			}
			r, err := in.Load()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serve(ctx, r.Value, in)
		},
	})
}

func serve(ctx context.Context, c settings, in *settingsInputs) (runErr error) {
	ids, err := id.NewSnowflake(c.Framework.ID.Node)
	if err != nil {
		return err
	}
	opts, err := startup.LoggerOptions(startup.LogSettings{Service: c.Framework.Service, Log: c.Framework.Log})
	if err != nil {
		return err
	}
	logs, err := logger.Open(opts)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, logs.Close()) }()
	metadata, err := c.Framework.Service.Metadata()
	if err != nil {
		return err
	}
	lg := slog.New(logs.Logger().Handler().WithAttrs(metadata.Attrs()))
	manager, err := newSettingsManager(c, func(level string) { _ = logs.SetLevel(level) })
	if err != nil {
		return err
	}
	checker, err := health.New(health.Options{})
	if err != nil {
		return err
	}
	r := router.New(lg, router.Options{Timeout: c.Framework.HTTP.Timeout, QuietHealth: true, Health: checker, Trace: []trace.Option{trace.WithSnowflake(ids)}})
	r.Get("/hello", respond.Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		snapshot := manager.Snapshot()
		return struct {
			Message       string   `json:"message"`
			Labels        []string `json:"labels"`
			ConfigVersion uint64   `json:"config_version"`
		}{snapshot.Value.Business.Message, snapshot.Value.Business.Labels, snapshot.Version}, nil
	}))
	s := server.New(net.JoinHostPort(c.Host, c.Framework.HTTP.Port), r)
	var service app.Service = s
	if in.watch {
		task, err := app.NewTask(func(ctx context.Context) error { return watchSettings(ctx, in, manager, lg) })
		if err != nil {
			return err
		}
		group, err := app.NewGroup(app.NamedService{Name: "http", Service: s}, app.NamedService{Name: "config-watch", Service: task})
		if err != nil {
			return err
		}
		service = group
	}
	l := c.Framework.Lifecycle
	a, err := app.New(service, app.Options{StartupTimeout: l.StartupTimeout, ShutdownTimeout: l.ShutdownTimeout, ShutdownDelay: l.ShutdownDelay, CleanupReserve: l.CleanupReserve, OnShutdown: func() { checker.SetReady(false) }})
	if err != nil {
		return err
	}
	return a.Run(ctx)
}
