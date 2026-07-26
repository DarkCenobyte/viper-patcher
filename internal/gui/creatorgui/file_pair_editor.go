package creatorgui

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/DarkCenobyte/viper-patcher/internal/gui/nativedialog"
	"github.com/DarkCenobyte/viper-patcher/internal/patch"
)

const (
	filePairTablePreferredHeight = 270
	filePairTableMinimumHeight   = 120
	filePairColumnWidth          = 430
)

type filePairEditor struct {
	model        filePairModel
	display      []filePairDisplay
	selected     int
	table        *widget.Table
	tableLayout  *fixedSizeLayout
	tableBox     *fyne.Container
	box          *fyne.Container
	window       fyne.Window
	addButton    *widget.Button
	removeButton *widget.Button
	clearButton  *widget.Button
}

func newFilePairEditor(window fyne.Window) *filePairEditor {
	editor := &filePairEditor{selected: -1, window: window}
	editor.table = widget.NewTable(
		func() (int, int) { return len(editor.display), 2 },
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Wrapping = fyne.TextWrapOff
			return label
		},
		func(id widget.TableCellID, object fyne.CanvasObject) {
			label := object.(*widget.Label)
			if id.Row < 0 || id.Row >= len(editor.display) {
				label.SetText("")
				return
			}
			if id.Col == 0 {
				label.SetText(editor.display[id.Row].Source)
				return
			}
			label.SetText(editor.display[id.Row].Target)
		},
	)
	editor.table.ShowHeaderRow = true
	editor.table.CreateHeader = func() fyne.CanvasObject {
		return widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	editor.table.UpdateHeader = func(id widget.TableCellID, object fyne.CanvasObject) {
		label := object.(*widget.Label)
		if id.Row != -1 {
			label.SetText("")
			return
		}
		if id.Col == 0 {
			label.SetText("Source")
			return
		}
		label.SetText("Target")
	}
	editor.table.SetColumnWidth(0, filePairColumnWidth)
	editor.table.SetColumnWidth(1, filePairColumnWidth)
	editor.table.OnSelected = func(id widget.TableCellID) {
		editor.selected = id.Row
	}
	editor.table.OnUnselected = func(widget.TableCellID) {
		editor.selected = -1
	}
	editor.addButton = widget.NewButton("Add file pair", editor.addPair)
	editor.removeButton = widget.NewButton("Remove selected", editor.removeSelected)
	editor.clearButton = widget.NewButton("Clear", editor.clear)
	editor.tableLayout = newFixedSizeLayout(fyne.NewSize(
		filePairColumnWidth*2+20,
		filePairTablePreferredHeight,
	))
	editor.tableBox = container.New(editor.tableLayout, editor.table)
	editor.box = container.NewBorder(
		nil,
		container.NewHBox(editor.addButton, editor.removeButton, editor.clearButton),
		nil,
		nil,
		editor.tableBox,
	)
	return editor
}

func (editor *filePairEditor) addPair() {
	nativedialog.OpenFile(editor.window, nativedialog.FileOptions{Title: "Select source file"}, func(sourcePath string, err error) {
		if err != nil {
			dialog.ShowError(err, editor.window)
			return
		}
		if sourcePath == "" {
			return
		}
		nativedialog.OpenFile(editor.window, nativedialog.FileOptions{
			Title:       "Select target file",
			InitialPath: filepath.Dir(sourcePath),
		}, func(targetPath string, targetErr error) {
			if targetErr != nil {
				dialog.ShowError(targetErr, editor.window)
				return
			}
			if targetPath == "" {
				return
			}
			if err := editor.model.Add(sourcePath, targetPath); err != nil {
				dialog.ShowError(err, editor.window)
				return
			}
			editor.refresh()
		})
	})
}

func (editor *filePairEditor) removeSelected() {
	if editor.model.Remove(editor.selected) {
		editor.selected = -1
		editor.table.UnselectAll()
		editor.refresh()
	}
}

func (editor *filePairEditor) clear() {
	editor.model.Clear()
	editor.selected = -1
	editor.table.UnselectAll()
	editor.refresh()
}

func (editor *filePairEditor) refresh() {
	editor.display = editor.model.DisplayPairs()
	editor.table.Refresh()
}

func (editor *filePairEditor) Pairs() []patch.FilePair { return editor.model.Pairs() }

func (editor *filePairEditor) Container() fyne.CanvasObject { return editor.box }

func (editor *filePairEditor) SetTableHeight(height float32) {
	if height < filePairTableMinimumHeight {
		height = filePairTableMinimumHeight
	}
	if height > filePairTablePreferredHeight {
		height = filePairTablePreferredHeight
	}
	editor.tableLayout.SetMinSize(fyne.NewSize(filePairColumnWidth*2+20, height))
	editor.tableBox.Refresh()
}

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
