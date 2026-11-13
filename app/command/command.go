// Package command dispatches standard service commands without opening resources,
// changing globals, installing signal handlers or exiting the process.
package command

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
)

// BuildInfo is normally supplied by the consumer's build-time variables.
type BuildInfo struct{ Version, Commit, BuildTime string }

// Options leaves configuration parsing and resource ownership with the consumer.
// Check receives true for explain, false for check. It must not open resources.
// Run receives only the arguments after the command; no arguments means run.
type Options struct {
	Build BuildInfo
	Run   func(context.Context, []string) error
	Check func(context.Context, []string, bool) error
	Out   io.Writer
}

// Execute supports run, version, config check and config explain. Errors contain
// no unrecognized command arguments, which may accidentally contain secrets.
// Callback errors are returned unchanged; callers own their safety and exit policy.
func Execute(ctx context.Context, args []string, o Options) error {
	if ctx == nil {
		return errors.New("command: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name := "run"
	if len(args) > 0 {
		name, args = args[0], args[1:]
	}
	switch name {
	case "run":
		if o.Run == nil {
			return errors.New("command: run callback is required")
		}
		return o.Run(ctx, args)
	case "version":
		if len(args) != 0 {
			return errors.New("command: version accepts no arguments")
		}
		if o.Out == nil {
			return errors.New("command: version output is required")
		}
		b := o.Build
		if b.Version == "" {
			b.Version = "dev"
		}
		if b.Commit == "" {
			b.Commit = "unknown"
		}
		if b.BuildTime == "" {
			b.BuildTime = "unknown"
		}
		return json.NewEncoder(o.Out).Encode(struct {
			Version   string `json:"version"`
			Commit    string `json:"commit"`
			BuildTime string `json:"build_time"`
			GoVersion string `json:"go_version"`
		}{b.Version, b.Commit, b.BuildTime, runtime.Version()})
	case "config":
		if len(args) == 0 || (args[0] != "check" && args[0] != "explain") {
			return errors.New("command: expected config check or config explain")
		}
		if o.Check == nil {
			return errors.New("command: config check callback is required")
		}
		return o.Check(ctx, args[1:], args[0] == "explain")
	default:
		return errors.New("command: expected run, version, config check or config explain")
	}
}
