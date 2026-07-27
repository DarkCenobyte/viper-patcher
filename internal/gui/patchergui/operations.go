package patchergui

import (
	"context"
	"errors"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func (controller *patcherController) cancelValidation() {
	controller.validationGeneration++
	if controller.validationCancel != nil {
		controller.validationCancel()
		controller.validationCancel = nil
	}
}

func (controller *patcherController) validate() {
	if controller.state.Active() {
		return
	}
	controller.cancelValidation()
	selection := controller.state.Snapshot()
	controller.setDirectionButtons(false, false)
	if selection.patchPath == "" || selection.targetDirectory == "" {
		controller.status.SetText("Select a patch and a target directory.")
		controller.growWindowToFitContent()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	controller.validationCancel = cancel
	generation := controller.validationGeneration
	controller.status.SetText("Inspecting installed files...")
	controller.growWindowToFitContent()

	go func() {
		result, err := patch.InspectContext(ctx, selection.targetDirectory, selection.parsed)
		fyne.Do(func() {
			if generation != controller.validationGeneration || controller.state.Active() {
				return
			}
			controller.validationCancel = nil
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				controller.status.SetText("Preflight inspection failed.")
				dialog.ShowError(err, controller.window)
				controller.growWindowToFitContent()
				return
			}
			controller.setDirectionButtons(result.CanApplyForward, result.CanApplyReverse)
			controller.status.SetText(patcherValidationText(result))
			controller.growWindowToFitContent()
		})
	}()
}

func (controller *patcherController) runDirection(direction patch.Direction) {
	selection, err := controller.state.Begin(direction)
	if err != nil {
		dialog.ShowError(err, controller.window)
		return
	}
	controller.cancelValidation()
	controller.setSelectionEnabled(false)
	controller.progressBar.SetValue(0)
	controller.progressBar.Show()
	controller.status.SetText(fmt.Sprintf("Preparing %s patch...", direction))
	controller.growWindowToFitContent()

	go func(snapshot patcherSelection) {
		err := applyPatcherSelection(snapshot, direction, func(event progress.Event) {
			fyne.Do(func() {
				controller.status.SetText(patcherProgressText(event, direction))
				controller.progressBar.SetValue(event.Overall)
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

			canForward, canReverse := directionsAfterApply(snapshot.parsed, direction)
			controller.setDirectionButtons(canForward, canReverse)
			controller.progressBar.SetValue(1)
			controller.status.SetText(fmt.Sprintf("%s patch applied successfully.", direction))
			controller.growWindowToFitContent()
			if err != nil {
				dialog.ShowInformation("Operation completed with warning", fmt.Sprintf("The %s patch was applied successfully, but cleanup reported a warning:\n\n%s", direction, err), controller.window)
			} else {
				dialog.ShowInformation("Operation completed", fmt.Sprintf("The %s patch was applied successfully.", direction), controller.window)
			}
		})
	}(selection)
}

func applyPatcherSelection(snapshot patcherSelection, direction patch.Direction, callback progress.Callback) error {
	if snapshot.prepared != nil {
		return patch.ApplyPreparedWithOptions(context.Background(), snapshot.prepared, patch.PreparedApplyOptions{
			Root:      snapshot.targetDirectory,
			Direction: direction,
		}, callback)
	}
	return patch.ApplyWithOptions(context.Background(), patch.ApplyOptions{
		PatchPath:         snapshot.patchPath,
		Root:              snapshot.targetDirectory,
		Direction:         direction,
		ExpectedPatchHash: snapshot.patchHash,
	}, callback)
}

func directionsAfterApply(parsed patchformat.Patch, direction patch.Direction) (canForward, canReverse bool) {
	statesIdentical := true
	for _, entry := range parsed.Header.Files {
		if entry.SourceSize != entry.TargetSize || entry.SourceHash != entry.TargetHash {
			statesIdentical = false
			break
		}
	}
	if direction == patch.Reverse {
		return true, parsed.Header.Reverse && statesIdentical
	}
	return statesIdentical, parsed.Header.Reverse
}

func (controller *patcherController) setDirectionButtons(canForward, canReverse bool) {
	controller.patchButton.Disable()
	controller.reverseButton.Disable()
	if canForward {
		controller.patchButton.Enable()
	}
	if canReverse {
		controller.reverseButton.Enable()
	}
}

func (controller *patcherController) setSelectionEnabled(enabled bool) {
	if enabled {
		controller.selectPatch.Enable()
		controller.selectDirectory.Enable()
		return
	}
	controller.selectPatch.Disable()
	controller.selectDirectory.Disable()
	controller.setDirectionButtons(false, false)
}
