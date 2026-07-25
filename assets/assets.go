// Package assets contains graphical resources embedded in the executables.
package assets

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed icon.png
var appIconData []byte

//go:embed logo.png
var appLogoData []byte

// AppIcon is the application icon shared by creator and patcher.
var AppIcon fyne.Resource = fyne.NewStaticResource(
	"icon.png",
	appIconData,
)

// AppLogo is the default logo shared by creator and patcher.
var AppLogo fyne.Resource = fyne.NewStaticResource(
	"logo.png",
	appLogoData,
)
