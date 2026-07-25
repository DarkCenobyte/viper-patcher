package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/appmode"
	"github.com/DarkCenobyte/viper-patcher/internal/cli/creatorcli"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/creatorgui"
)

func main() {
	arguments := os.Args[1:]
	guiAvailable := appmode.GUIAvailable()
	if appmode.HeadlessRequested(arguments) || appmode.CLIRequested(arguments) || !guiAvailable {
		if !guiAvailable && !appmode.HeadlessRequested(arguments) {
			fmt.Fprintln(os.Stderr, "Warning: no graphical environment detected; falling back to command-line mode.")
		}
		os.Exit(creatorcli.Run(context.Background(), arguments, os.Stdout, os.Stderr))
	}
	appmode.PrepareGUI()
	creatorgui.Run()
}
