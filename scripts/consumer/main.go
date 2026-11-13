// Command consumer verifies independent Frame consumer modules.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maxtanforge/go-frame/internal/consumer"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__consumer_child" {
		os.Exit(consumer.ChildMain(os.Args[2:]))
	}
	var o consumer.Options
	flags := flag.NewFlagSet("consumer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.Repo, "repo", "", "framework repository path")
	flags.StringVar(&o.Mode, "mode", "workspace", "workspace or remote")
	flags.StringVar(&o.Version, "version", "", "concrete remote version or commit")
	flags.BoolVar(&o.Network, "network", false, "allow network dependency downloads")
	flags.BoolVar(&o.Process, "process", false, "run loopback process checks")
	flags.BoolVar(&o.Race, "race", false, "require race checks")
	flags.StringVar(&o.Engine, "engine", "", "explicit mysql or postgres integration check")
	flags.DurationVar(&o.Budget, "budget", 10*time.Minute, "total budget (at most 10m)")
	if e := flags.Parse(os.Args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			flags.SetOutput(os.Stdout)
			flags.PrintDefaults()
			return
		}
		fmt.Fprintln(os.Stderr, "invalid flags; use --help")
		os.Exit(2)
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := consumer.Run(ctx, o, os.Stderr)
	if e := json.NewEncoder(os.Stdout).Encode(report); e != nil {
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
}
