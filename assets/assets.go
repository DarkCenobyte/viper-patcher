// Package assets contains graphical resources embedded in the executables.
package assets

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed icon.png
var appIconData []byte

// AppIcon is the application icon shared by creator and patcher.
var AppIcon fyne.Resource = fyne.NewStaticResource(
	"icon.png",
	appIconData,
)
