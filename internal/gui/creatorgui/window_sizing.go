package creatorgui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/theme"

	"github.com/go-gl/glfw/v3.4/glfw"
)

const (
	creatorWindowScreenRatio         = 0.90
	creatorWindowDecorationAllowance = 32
	creatorWindowFallbackHeight      = 940
)

func maximumCreatorContentHeight(window fyne.Window) float32 {
	maximum := float32(creatorWindowFallbackHeight)
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

	maximumPixels := float32(screenHeight) * creatorWindowScreenRatio
	if workAreaHeight > 0 && float32(workAreaHeight) < maximumPixels {
		maximumPixels = float32(workAreaHeight)
	}
	scale := window.Canvas().Scale()
	if scale <= 0 {
		scale = 1
	}
	maximum = maximumPixels/scale - creatorWindowDecorationAllowance
	if maximum < 320 {
		return 320
	}
	return maximum
}

func preferredCreatorContentHeight(header, body, footer fyne.CanvasObject) float32 {
	// BorderLayout adds one padding gap below the header and one above the footer.
	// The window canvas adds its normal outer padding around the root object too.
	return header.MinSize().Height + body.MinSize().Height + footer.MinSize().Height + 4*theme.Padding()
}

func fittedFilePairTableHeight(preferred, minimum, desiredWindowHeight, maximumWindowHeight float32) float32 {
	if desiredWindowHeight <= maximumWindowHeight {
		return preferred
	}
	height := preferred - (desiredWindowHeight - maximumWindowHeight)
	if height < minimum {
		return minimum
	}
	if height > preferred {
		return preferred
	}
	return height
}
