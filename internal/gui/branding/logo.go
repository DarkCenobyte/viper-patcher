// Package branding provides shared graphical branding helpers.
package branding

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// NewLogo creates a centered image that preserves the logo aspect ratio.
func NewLogo(resource fyne.Resource, size fyne.Size) fyne.CanvasObject {
	image := canvas.NewImageFromResource(resource)
	image.FillMode = canvas.ImageFillContain
	image.SetMinSize(size)
	return container.NewCenter(image)
}
