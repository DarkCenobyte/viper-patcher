package patchergui

import (
	"context"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/DarkCenobyte/viper-patcher/assets"
	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

type patcherController struct {
	window          fyne.Window
	state           *patcherState
	patchLabel      *widget.Label
	directoryLabel  *widget.Label
	comment         *widget.Label
	status          *widget.Label
	progressBar     *widget.ProgressBar
	patchButton     *widget.Button
	reverseButton   *widget.Button
	selectPatch     *widget.Button
	selectDirectory *widget.Button
}

func newPatcherController(application fyne.App) *patcherController {
	controller := &patcherController{state: &patcherState{}}
	controller.window = application.NewWindow("Viper Patcher")
	controller.window.SetIcon(assets.AppIcon)
	controller.window.Resize(fyne.NewSize(820, 640))

	controller.patchLabel = widget.NewLabel("No patch selected")
	controller.patchLabel.Wrapping = fyne.TextWrapWord
	controller.directoryLabel = widget.NewLabel("No target directory selected")
	controller.directoryLabel.Wrapping = fyne.TextWrapWord
	controller.comment = widget.NewLabel("Select a .vipr patch to display its embedded instructions and comment.")
	controller.comment.Wrapping = fyne.TextWrapWord
	controller.status = widget.NewLabel("Select a patch and a target directory.")
	controller.status.Wrapping = fyne.TextWrapWord
	controller.progressBar = widget.NewProgressBar()
	controller.progressBar.Hide()
	controller.patchButton = widget.NewButton("Apply forward patch", func() { controller.runDirection(patch.Forward) })
	controller.patchButton.Importance = widget.HighImportance
	controller.reverseButton = widget.NewButton("Apply reverse patch", func() { controller.runDirection(patch.Reverse) })
	controller.patchButton.Disable()
	controller.reverseButton.Disable()
	controller.selectPatch = widget.NewButton("Select patch", controller.choosePatch)
	controller.selectPatch.Importance = widget.MediumImportance
	controller.selectDirectory = widget.NewButton("Select target folder", controller.chooseTargetDirectory)
	controller.selectDirectory.Importance = widget.MediumImportance
	controller.window.SetContent(controller.buildContent())
	return controller
}

func (controller *patcherController) buildContent() fyne.CanvasObject {
	selectionCard := widget.NewCard(
		"Patch and target selection",
		"Selections are locked while an operation is active.",
		container.NewVBox(
			container.NewBorder(nil, nil, controller.selectPatch, nil, controller.patchLabel),
			container.NewBorder(nil, nil, controller.selectDirectory, nil, controller.directoryLabel),
		),
	)
	commentCard := widget.NewCard(
		"Patch instructions / comment",
		"Read the creator-provided instructions before applying the patch.",
		container.NewVScroll(controller.comment),
	)
	operationCard := widget.NewCard(
		"Preflight and operation",
		"Actions are enabled only when every file matches the required hash and permission mode.",
		container.NewVBox(controller.status, controller.progressBar, container.NewGridWithColumns(2, controller.patchButton, controller.reverseButton)),
	)
	return container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("Apply a VIPR differential patch", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewLabel("The patch and installed files are snapshotted before native processing."),
		),
		nil,
		nil,
		nil,
		container.NewVScroll(container.NewVBox(selectionCard, commentCard, operationCard)),
	)
}

func (controller *patcherController) choosePatch() {
	picker := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, controller.window)
			return
		}
		if reader == nil {
			return
		}
		path := reader.URI().Path()
		if err := reader.Close(); err != nil {
			dialog.ShowError(fmt.Errorf("close selected patch file: %w", err), controller.window)
			return
		}
		parsed, digest, err := patch.OpenWithDigest(path)
		if err != nil {
			dialog.ShowError(err, controller.window)
			return
		}
		if !controller.state.SetPatch(path, digest, parsed) {
			return
		}
		controller.patchLabel.SetText(path)
		if parsed.Header.Comment == "" {
			controller.comment.SetText("This patch does not contain a comment.")
		} else {
			controller.comment.SetText(parsed.Header.Comment)
		}
		controller.validate()
	}, controller.window)
	picker.SetFilter(storage.NewExtensionFileFilter([]string{".vipr"}))
	picker.Show()
}

func (controller *patcherController) chooseTargetDirectory() {
	dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, controller.window)
			return
		}
		if uri == nil {
			return
		}
		if !controller.state.SetTargetDirectory(uri.Path()) {
			return
		}
		controller.directoryLabel.SetText(uri.Path())
		controller.validate()
	}, controller.window).Show()
}

func (controller *patcherController) validate() {
	if controller.state.Active() {
		return
	}
	selection := controller.state.Snapshot()
	controller.patchButton.Disable()
	controller.reverseButton.Disable()
	if selection.patchPath == "" || selection.targetDirectory == "" {
		controller.status.SetText("Select a patch and a target directory.")
		return
	}
	result, err := patch.Inspect(selection.targetDirectory, selection.parsed)
	if err != nil {
		controller.status.SetText("Preflight inspection failed.")
		dialog.ShowError(err, controller.window)
		return
	}
	if result.CanApplyForward {
		controller.patchButton.Enable()
	}
	if result.CanApplyReverse {
		controller.reverseButton.Enable()
	}
	controller.status.SetText(patcherValidationText(result))
}

func (controller *patcherController) runDirection(direction patch.Direction) {
	selection, err := controller.state.Begin(direction)
	if err != nil {
		dialog.ShowError(err, controller.window)
		return
	}
	controller.setSelectionEnabled(false)
	controller.progressBar.SetValue(0)
	controller.progressBar.Show()
	controller.status.SetText(fmt.Sprintf("Preparing %s patch...", direction))

	go func(snapshot patcherSelection) {
		err := patch.ApplyWithOptions(context.Background(), patch.ApplyOptions{
			PatchPath:         snapshot.patchPath,
			Root:              snapshot.targetDirectory,
			Direction:         direction,
			ExpectedPatchHash: snapshot.patchHash,
		}, func(event progress.Event) {
			fyne.Do(func() {
				controller.status.SetText(patcherProgressText(event, direction))
				controller.progressBar.SetValue(patcherOverallProgress(event))
			})
		})
		fyne.Do(func() {
			controller.state.End()
			controller.setSelectionEnabled(true)
			if err != nil {
				controller.status.SetText("Patch operation failed.")
				dialog.ShowError(err, controller.window)
				controller.validate()
				return
			}
			controller.progressBar.SetValue(1)
			controller.status.SetText(fmt.Sprintf("%s patch applied successfully.", direction))
			dialog.ShowInformation("Operation completed", fmt.Sprintf("The %s patch was applied successfully.", direction), controller.window)
			controller.validate()
		})
	}(selection)
}

func (controller *patcherController) setSelectionEnabled(enabled bool) {
	if enabled {
		controller.selectPatch.Enable()
		controller.selectDirectory.Enable()
		return
	}
	controller.selectPatch.Disable()
	controller.selectDirectory.Disable()
	controller.patchButton.Disable()
	controller.reverseButton.Disable()
}
