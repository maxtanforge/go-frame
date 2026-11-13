// Command api runs the category business example.
package main

import (
	"context"
	"os"

	"github.com/maxtanforge/go-frame/examples/business/internal/bootstrap"
)

func main() {
	if err := bootstrap.Run(context.Background()); err != nil {
		os.Exit(1)
	}
}
