package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/appmode"
	"github.com/DarkCenobyte/viper-patcher/internal/cli/creatorcli"
	"github.com/DarkCenobyte/viper-patcher/internal/commandctx"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/creatorgui"
)

func main() {
	arguments := os.Args[1:]
	guiAvailable := appmode.GUIAvailable()
	if appmode.HeadlessRequested(arguments) || appmode.CLIRequested(arguments) || !guiAvailable {
		os.Exit(runCLI(arguments, guiAvailable))
	}
	appmode.PrepareGUI()
	creatorgui.Run()
}

func runCLI(arguments []string, guiAvailable bool) int {
	if !guiAvailable && !appmode.HeadlessRequested(arguments) {
		fmt.Fprintln(os.Stderr, "Warning: no graphical environment detected; falling back to command-line mode.")
	}
	ctx, stop := commandctx.New(context.Background())
	defer stop()
	return creatorcli.Run(ctx, arguments, os.Stdout, os.Stderr)
}
