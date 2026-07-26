package creatorgui

import "fyne.io/fyne/v2"

type fixedSizeLayout struct {
	minSize fyne.Size
}

func newFixedSizeLayout(minSize fyne.Size) *fixedSizeLayout {
	return &fixedSizeLayout{minSize: minSize}
}

func (layout *fixedSizeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Resize(size)
		object.Move(fyne.NewPos(0, 0))
	}
}

func (layout *fixedSizeLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return layout.minSize
}

func (layout *fixedSizeLayout) SetMinSize(size fyne.Size) {
	layout.minSize = size
}
