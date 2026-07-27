package patchergui

import (
	"fyne.io/fyne/v2/app"

	"github.com/DarkCenobyte/viper-patcher/assets"
)

// Run starts the patcher graphical interface.
func Run() {
	application := app.NewWithID("io.github.darkcenobyte.viperpatcher.patcher")
	application.SetIcon(assets.AppIcon)
	controller := newPatcherController(application)
	controller.window.Show()
	controller.fitInitialWindow()
	application.Run()
	controller.close()
}
