package patchergui

import (
	"context"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/DarkCenobyte/viper-patcher/assets"
	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

// Run starts the patcher graphical interface.
func Run() {
	application := app.NewWithID("io.github.darkcenobyte.viperpatcher.patcher")
	application.SetIcon(assets.AppIcon)
	window := application.NewWindow("Viper Patcher")
	window.SetIcon(assets.AppIcon)
	window.Resize(fyne.NewSize(760, 560))

	patchPath := ""
	targetDirectory := ""
	var parsed patchformat.Patch
	patchLabel := widget.NewLabel("No patch selected")
	directoryLabel := widget.NewLabel("No target directory selected")
	comment := widget.NewLabel("Select a .vipr patch first to display its instructions and comment.")
	comment.Wrapping = fyne.TextWrapWord
	status := widget.NewLabel("Select a patch and a target directory.")
	status.Wrapping = fyne.TextWrapWord
	progressBar := widget.NewProgressBar()
	progressBar.Hide()
	patchButton := widget.NewButton("Patch", nil)
	reverseButton := widget.NewButton("Reverse", nil)
	patchButton.Importance = widget.HighImportance
	patchButton.Disable()
	reverseButton.Disable()

	validate := func() {
		patchButton.Disable()
		reverseButton.Disable()
		if patchPath == "" || targetDirectory == "" {
			status.SetText("Select a patch and a target directory.")
			return
		}
		result, err := patch.Inspect(targetDirectory, parsed)
		if err != nil {
			status.SetText("Validation failed: " + err.Error())
			return
		}
		switch result.State {
		case patch.StateForwardReady:
			status.SetText("All source files are present and match. The patch can be applied.")
			patchButton.Enable()
		case patch.StateReverseReady:
			status.SetText("The patched files match. The reverse patch can be applied.")
			reverseButton.Enable()
		case patch.StateMissingFiles, patch.StateHashMismatch:
			status.SetText(result.Error().Error())
		}
	}

	selectPatch := widget.NewButton("Select patch", func() {
		dialogInstance := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			if reader == nil {
				return
			}
			selected := reader.URI().Path()
			_ = reader.Close()
			opened, err := patch.Open(selected)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			patchPath = selected
			parsed = opened
			patchLabel.SetText(selected)
			comment.SetText(opened.Header.Comment)
			validate()
		}, window)
		dialogInstance.SetFilter(storage.NewExtensionFileFilter([]string{".vipr"}))
		dialogInstance.Show()
	})
	selectDirectory := widget.NewButton("Select target directory", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			if uri == nil {
				return
			}
			targetDirectory = uri.Path()
			directoryLabel.SetText(targetDirectory)
			validate()
		}, window)
	})

	runDirection := func(direction patch.Direction) {
		patchButton.Disable()
		reverseButton.Disable()
		progressBar.SetValue(0)
		progressBar.Show()
		status.SetText(fmt.Sprintf("Applying %s patch...", direction))
		go func() {
			err := patch.Apply(context.Background(), patchPath, targetDirectory, direction, func(event progress.Event) {
				fyne.Do(func() {
					status.SetText(progressText(event, direction))
					progressBar.SetValue(overallProgress(event))
				})
			})
			fyne.Do(func() {
				if err != nil {
					status.SetText("Patch operation failed.")
					dialog.ShowError(err, window)
					validate()
					return
				}
				progressBar.SetValue(1)
				status.SetText(fmt.Sprintf("%s patch applied successfully.", direction))
				dialog.ShowInformation("Operation completed", fmt.Sprintf("The %s patch was applied successfully.", direction), window)
				validate()
			})
		}()
	}
	patchButton.OnTapped = func() { runDirection(patch.Forward) }
	reverseButton.OnTapped = func() { runDirection(patch.Reverse) }

	content := container.NewVBox(
		widget.NewLabelWithStyle("Apply a VIPR differential patch", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, selectPatch, nil, patchLabel),
		container.NewBorder(nil, nil, selectDirectory, nil, directoryLabel),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Patch instructions / comment", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewVScroll(comment),
		widget.NewSeparator(),
		status,
		progressBar,
		container.NewGridWithColumns(2, patchButton, reverseButton),
	)
	window.SetContent(content)
	window.ShowAndRun()
}

func progressText(event progress.Event, direction patch.Direction) string {
	if event.Stage == "completed" {
		return fmt.Sprintf("Finalizing %s patch...", direction)
	}
	return fmt.Sprintf("[%d/%d] Applying %s patch: %s", event.FileIndex, event.FileCount, direction, event.Path)
}

func overallProgress(event progress.Event) float64 {
	if event.FileCount <= 0 {
		return 0
	}
	fileFraction := 0.0
	if event.TotalBytes > 0 {
		fileFraction = float64(event.ProcessedBytes) / float64(event.TotalBytes)
	}
	value := (float64(event.FileIndex-1) + fileFraction) / float64(event.FileCount)
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
