package creatorgui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
)

type filePairEditor struct {
	model        filePairModel
	selected     int
	list         *widget.List
	box          *fyne.Container
	window       fyne.Window
	addButton    *widget.Button
	removeButton *widget.Button
	clearButton  *widget.Button
}

func newFilePairEditor(window fyne.Window) *filePairEditor {
	editor := &filePairEditor{selected: -1, window: window}
	editor.list = widget.NewList(
		func() int { return editor.model.Len() },
		func() fyne.CanvasObject {
			source := widget.NewLabel("")
			source.Wrapping = fyne.TextWrapOff
			target := widget.NewLabel("")
			target.Wrapping = fyne.TextWrapOff
			return container.NewVBox(source, target)
		},
		func(id widget.ListItemID, object fyne.CanvasObject) {
			pair := editor.model.Pairs()[id]
			labels := object.(*fyne.Container).Objects
			labels[0].(*widget.Label).SetText("Source: " + pair.SourcePath)
			labels[1].(*widget.Label).SetText("Target: " + pair.TargetPath)
		},
	)
	editor.list.OnSelected = func(id widget.ListItemID) { editor.selected = id }
	editor.list.OnUnselected = func(widget.ListItemID) { editor.selected = -1 }
	editor.addButton = widget.NewButton("Add file pair", editor.addPair)
	editor.removeButton = widget.NewButton("Remove selected", editor.removeSelected)
	editor.clearButton = widget.NewButton("Clear", editor.clear)
	editor.box = container.NewBorder(
		nil,
		container.NewHBox(editor.addButton, editor.removeButton, editor.clearButton),
		nil,
		nil,
		editor.list,
	)
	editor.box.Resize(fyne.NewSize(900, 300))
	return editor
}

func (editor *filePairEditor) addPair() {
	dialog.ShowFileOpen(func(source fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, editor.window)
			return
		}
		if source == nil {
			return
		}
		sourcePath := source.URI().Path()
		if err := source.Close(); err != nil {
			dialog.ShowError(fmt.Errorf("close selected source file: %w", err), editor.window)
			return
		}
		dialog.ShowFileOpen(func(target fyne.URIReadCloser, targetErr error) {
			if targetErr != nil {
				dialog.ShowError(targetErr, editor.window)
				return
			}
			if target == nil {
				return
			}
			targetPath := target.URI().Path()
			if err := target.Close(); err != nil {
				dialog.ShowError(fmt.Errorf("close selected target file: %w", err), editor.window)
				return
			}
			if err := editor.model.Add(sourcePath, targetPath); err != nil {
				dialog.ShowError(err, editor.window)
				return
			}
			editor.list.Refresh()
		}, editor.window)
	}, editor.window)
}

func (editor *filePairEditor) removeSelected() {
	if editor.model.Remove(editor.selected) {
		editor.selected = -1
		editor.list.UnselectAll()
		editor.list.Refresh()
	}
}

func (editor *filePairEditor) clear() {
	editor.model.Clear()
	editor.selected = -1
	editor.list.UnselectAll()
	editor.list.Refresh()
}

func (editor *filePairEditor) Pairs() []patch.FilePair { return editor.model.Pairs() }

func (editor *filePairEditor) Container() fyne.CanvasObject { return editor.box }

func (editor *filePairEditor) SetEnabled(enabled bool) {
	if enabled {
		editor.addButton.Enable()
		editor.removeButton.Enable()
		editor.clearButton.Enable()
		return
	}
	editor.addButton.Disable()
	editor.removeButton.Disable()
	editor.clearButton.Disable()
}
