package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/appmode"
	"github.com/DarkCenobyte/viper-patcher/internal/cli/patchercli"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/patchergui"
)

func main() {
	arguments := os.Args[1:]
	guiAvailable := appmode.GUIAvailable()
	if appmode.HeadlessRequested(arguments) || appmode.CLIRequested(arguments) || !guiAvailable {
		if !guiAvailable && !appmode.HeadlessRequested(arguments) {
			fmt.Fprintln(os.Stderr, "Warning: no graphical environment detected; falling back to command-line mode.")
		}
		os.Exit(patchercli.Run(context.Background(), arguments, os.Stdout, os.Stderr))
	}
	patchergui.Run()
}
