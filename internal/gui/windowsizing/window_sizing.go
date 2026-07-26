package windowsizing

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/theme"

	"github.com/go-gl/glfw/v3.4/glfw"
)

const (
	windowScreenRatio         = 0.90
	windowDecorationAllowance = 32
	minimumContentHeight      = 320
)

// MaximumContentHeight returns the largest Fyne canvas height that keeps the
// window below 90% of the primary screen and within the desktop work area.
func MaximumContentHeight(window fyne.Window, fallbackHeight float32) float32 {
	maximum := fallbackHeight
	nativeWindow, ok := window.(driver.NativeWindow)
	if !ok {
		return maximum
	}

	var screenHeight, workAreaHeight int
	nativeWindow.RunNative(func(any) {
		monitor := glfw.GetPrimaryMonitor()
		if monitor == nil {
			return
		}
		mode := monitor.GetVideoMode()
		if mode != nil {
			screenHeight = mode.Height
		}
		_, _, _, workAreaHeight = monitor.GetWorkarea()
	})
	if screenHeight <= 0 {
		return maximum
	}

	maximumPixels := float32(screenHeight) * windowScreenRatio
	if workAreaHeight > 0 && float32(workAreaHeight) < maximumPixels {
		maximumPixels = float32(workAreaHeight)
	}
	scale := window.Canvas().Scale()
	if scale <= 0 {
		scale = 1
	}
	maximum = maximumPixels/scale - windowDecorationAllowance
	if maximum < minimumContentHeight {
		return minimumContentHeight
	}
	return maximum
}

// PreferredBorderContentHeight estimates the canvas height required by a
// BorderLayout containing an optional header and footer around one body.
func PreferredBorderContentHeight(header, body, footer fyne.CanvasObject) float32 {
	height := float32(2) * theme.Padding()
	if body != nil {
		height += body.MinSize().Height
	}
	if header != nil {
		height += header.MinSize().Height + theme.Padding()
	}
	if footer != nil {
		height += footer.MinSize().Height + theme.Padding()
	}
	return height
}
