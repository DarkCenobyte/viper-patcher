// Command patcher-cli applies VIPR patches without linking the GUI stack.
package main

import (
	"context"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/cli/patchercli"
	"github.com/DarkCenobyte/viper-patcher/internal/commandctx"
)

func main() {
	ctx, stop := commandctx.New(context.Background())
	defer stop()
	os.Exit(patchercli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
