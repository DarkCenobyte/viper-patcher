// Command creator-cli creates VIPR patches without linking the GUI stack.
package main

import (
	"context"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/cli/creatorcli"
	"github.com/DarkCenobyte/viper-patcher/internal/commandctx"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := commandctx.New(context.Background())
	defer stop()
	return creatorcli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
}
