// Command frame runs local development tooling; see `frame dev`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maxtanforge/go-frame/internal/dev"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__frame_child" {
		os.Exit(dev.ChildMain(os.Args[2:]))
	}
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h" || os.Args[1] == "help") {
		if err := printUsage(os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}

	if len(os.Args) < 2 || os.Args[1] != "dev" {
		_ = printUsage(os.Stderr)
		os.Exit(2)
	}
	o, err := parseDev(os.Args[2:])
	if errors.Is(err, flag.ErrHelp) {
		if err := printUsage(os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "frame dev:", err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := dev.Run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "frame dev:", err)
		os.Exit(1)
	}
}

func printUsage(w io.Writer) error {
	_, err := fmt.Fprintln(w, "usage: frame dev [--dir directory] [--package package] [--interval 500ms] [--debounce 300ms] [--stop-timeout 12s] [-- app arguments]")
	return err
}
func parseDev(args []string) (dev.Options, error) {
	var o dev.Options
	flags := flag.NewFlagSet("frame dev", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.Dir, "dir", ".", "source root and app working directory")
	flags.StringVar(&o.Package, "package", "./examples/configured", "single Go main package")
	flags.DurationVar(&o.Interval, "interval", 500*time.Millisecond, "source scan interval")
	flags.DurationVar(&o.Debounce, "debounce", 300*time.Millisecond, "quiet period before rebuilding")
	flags.DurationVar(&o.StopTimeout, "stop-timeout", 12*time.Second, "process stop budget")
	separator := -1
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	flagArgs := args
	if separator >= 0 {
		flagArgs = args[:separator]
		o.Args = append([]string{}, args[separator+1:]...)
	}
	if err := flags.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, err
		}
		return o, errors.New("invalid flags; see frame dev --help")
	}
	if flags.NArg() != 0 {
		return o, fmt.Errorf("app arguments must follow --")
	}
	explicitPackage := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "package" {
			explicitPackage = true
		}
	})
	if separator < 0 && !explicitPackage {
		o.Args = []string{"run", "--config", "examples/configured/config.yaml", "--watch-config"}
	}
	if o.Dir == "" || o.Package == "" || o.Interval <= 0 || o.Debounce < 0 || o.StopTimeout <= 0 {
		return o, fmt.Errorf("dir/package must be nonempty; interval/stop-timeout must be positive; debounce must be nonnegative")
	}
	return o, nil
}
