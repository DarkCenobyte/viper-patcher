package creatorgui

import (
	"fyne.io/fyne/v2/app"

	"github.com/DarkCenobyte/viper-patcher/assets"
)

// Run starts the creator graphical interface.
func Run() {
	application := app.NewWithID("io.github.darkcenobyte.viperpatcher.creator")
	application.SetIcon(assets.AppIcon)
	controller := newCreatorController(application)
	controller.window.Show()
	controller.fitInitialWindow()
	application.Run()
}
