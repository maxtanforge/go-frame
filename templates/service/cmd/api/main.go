package main

import (
	"context"
	"fmt"
	"os"

	"example.com/service/internal/bootstrap"
	"github.com/maxtanforge/go-frame/app/command"
)

var version = "dev"
var commit = "unknown"
var buildTime = "unknown"

func main() {
	if err := bootstrap.Execute(context.Background(), os.Args[1:], os.Stdout, os.LookupEnv, command.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime}); err != nil {
		// Runtime errors already have structured logging. Inspection errors need stderr.
		if len(os.Args) > 1 && os.Args[1] != "run" {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
