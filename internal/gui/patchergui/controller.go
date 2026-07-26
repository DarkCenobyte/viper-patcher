package patchergui

import (
	"context"
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/DarkCenobyte/viper-patcher/assets"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/branding"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/nativedialog"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/windowsizing"
	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

const (
	patcherWindowWidth          = 860
	patcherWindowFallbackHeight = 760
	patcherLogoWidth            = 320
	patcherLogoHeight           = 148
)

type patcherController struct {
	window          fyne.Window
	header          fyne.CanvasObject
	body            fyne.CanvasObject
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
	logo            fyne.Resource
}

func newPatcherController(application fyne.App) *patcherController {
	executablePath, _ := os.Executable()
	controller := &patcherController{
		state: &patcherState{},
		logo:  loadPatcherLogo(executablePath),
	}
	controller.window = application.NewWindow("Viper Patcher")
	controller.window.SetIcon(assets.AppIcon)
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
	controller.selectAdjacentPatch(executablePath)
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
		"Actions are enabled only when every file matches the required hash and size.",
		container.NewVBox(controller.status, controller.progressBar, container.NewGridWithColumns(2, controller.patchButton, controller.reverseButton)),
	)
	controller.header = container.NewVBox(
		branding.NewLogo(controller.logo, fyne.NewSize(patcherLogoWidth, patcherLogoHeight)),
		widget.NewLabelWithStyle("Apply a VIPR differential patch", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("The patch and installed files are snapshotted before native processing.", fyne.TextAlignCenter, fyne.TextStyle{}),
	)
	controller.body = container.NewVBox(selectionCard, commentCard, operationCard)
	return container.NewBorder(
		controller.header,
		nil,
		nil,
		nil,
		container.NewVScroll(controller.body),
	)
}

func (controller *patcherController) fitInitialWindow() {
	maximumHeight := windowsizing.MaximumContentHeight(controller.window, patcherWindowFallbackHeight)
	desiredHeight := windowsizing.PreferredBorderContentHeight(controller.header, controller.body, nil)
	if desiredHeight < patcherWindowFallbackHeight {
		desiredHeight = patcherWindowFallbackHeight
	}
	if desiredHeight > maximumHeight {
		desiredHeight = maximumHeight
	}

	controller.window.Resize(fyne.NewSize(patcherWindowWidth, desiredHeight))
	controller.window.CenterOnScreen()
}

func (controller *patcherController) growWindowToFitContent() {
	desiredHeight := windowsizing.PreferredBorderContentHeight(controller.header, controller.body, nil)
	maximumHeight := windowsizing.MaximumContentHeight(controller.window, patcherWindowFallbackHeight)
	if desiredHeight > maximumHeight {
		desiredHeight = maximumHeight
	}

	currentSize := controller.window.Canvas().Size()
	if desiredHeight <= currentSize.Height {
		return
	}
	width := currentSize.Width
	if width < patcherWindowWidth {
		width = patcherWindowWidth
	}
	controller.window.Resize(fyne.NewSize(width, desiredHeight))
}

func (controller *patcherController) choosePatch() {
	selection := controller.state.Snapshot()
	nativedialog.OpenFile(controller.window, nativedialog.FileOptions{
		Title:       "Select VIPR patch",
		InitialPath: selection.patchPath,
		Extensions:  []string{".vipr"},
	}, func(path string, err error) {
		if err != nil {
			dialog.ShowError(err, controller.window)
			return
		}
		if path == "" {
			return
		}
		if err := controller.loadPatch(path); err != nil {
			dialog.ShowError(err, controller.window)
		}
	})
}

func (controller *patcherController) chooseTargetDirectory() {
	selection := controller.state.Snapshot()
	nativedialog.OpenDirectory(controller.window, nativedialog.DirectoryOptions{
		Title:       "Select target folder",
		InitialPath: selection.targetDirectory,
	}, func(path string, err error) {
		if err != nil {
			dialog.ShowError(err, controller.window)
			return
		}
		if path == "" {
			return
		}
		if !controller.state.SetTargetDirectory(path) {
			return
		}
		controller.directoryLabel.SetText(path)
		controller.validate()
	})
}

func (controller *patcherController) loadPatch(path string) error {
	parsed, digest, err := patch.OpenWithDigest(path)
	if err != nil {
		return err
	}
	if !controller.state.SetPatch(path, digest, parsed) {
		return fmt.Errorf("patch selection is locked while an operation is active")
	}
	controller.patchLabel.SetText(path)
	if parsed.Header.Comment == "" {
		controller.comment.SetText("This patch does not contain a comment.")
	} else {
		controller.comment.SetText(parsed.Header.Comment)
	}
	controller.validate()
	return nil
}

func (controller *patcherController) selectAdjacentPatch(executablePath string) {
	path, found, err := adjacentPatchPath(executablePath)
	if err != nil {
		controller.status.SetText("Could not inspect the application directory for an adjacent patch.")
		return
	}
	if !found {
		return
	}
	if err := controller.loadPatch(path); err != nil {
		controller.status.SetText("The adjacent VIPR patch could not be opened.")
	}
}

func (controller *patcherController) validate() {
	if controller.state.Active() {
		return
	}
	defer controller.growWindowToFitContent()
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
	controller.growWindowToFitContent()

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
				controller.growWindowToFitContent()
			})
		})
		fyne.Do(func() {
			controller.state.End()
			controller.setSelectionEnabled(true)
			if err != nil && !patch.IsCommittedWarning(err) {
				controller.status.SetText("Patch operation failed.")
				dialog.ShowError(err, controller.window)
				controller.validate()
				return
			}
			controller.progressBar.SetValue(1)
			controller.status.SetText(fmt.Sprintf("%s patch applied successfully.", direction))
			if err != nil {
				dialog.ShowInformation("Operation completed with warning", fmt.Sprintf("The %s patch was applied successfully, but cleanup reported a warning:\n\n%s", direction, err), controller.window)
			} else {
				dialog.ShowInformation("Operation completed", fmt.Sprintf("The %s patch was applied successfully.", direction), controller.window)
			}
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
