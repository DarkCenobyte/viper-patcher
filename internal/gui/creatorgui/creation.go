package creatorgui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/workerbudget"
)

func (controller *creatorController) createPatch() {
	options, err := controller.createOptions()
	if err != nil {
		dialog.ShowError(err, controller.window)
		return
	}
	estimate, err := patch.EstimateCreate(options)
	if err != nil {
		dialog.ShowError(err, controller.window)
		return
	}
	message := fmt.Sprintf(
		"Estimated peak temporary disk usage: %s\n\nEstimated creator work usage: %s\nEstimated output-folder usage: %s\n\nThe estimate is conservative and includes snapshots, differential bounds, the temporary patch, and an existing output backup. Continue?",
		formatByteSize(estimate.TotalBytes),
		formatByteSize(estimate.WorkDirectoryBytes),
		formatByteSize(estimate.OutputDirectoryBytes),
	)
	dialog.NewConfirm("Confirm patch creation", message, func(confirmed bool) {
		if confirmed {
			controller.startCreate(options)
		}
	}, controller.window).Show()
}

func (controller *creatorController) startCreate(options patch.CreateOptions) {
	controller.setControlsEnabled(false)
	controller.progressBar.SetValue(0)
	controller.progressBar.Show()
	controller.status.SetText("Preparing immutable input snapshots...")

	go func(options patch.CreateOptions) {
		err := patch.Create(context.Background(), options, func(event progress.Event) {
			fyne.Do(func() {
				controller.status.SetText(creatorProgressText(event))
				controller.progressBar.SetValue(event.Overall)
			})
		})
		fyne.Do(func() {
			controller.setControlsEnabled(true)
			if err != nil && !patch.IsCommittedWarning(err) {
				controller.status.SetText("Patch creation failed.")
				dialog.ShowError(err, controller.window)
				return
			}
			controller.progressBar.SetValue(1)
			controller.status.SetText("Patch created successfully: " + options.OutputPath)
			if err != nil {
				dialog.ShowInformation("Patch created with warning", "The VIPR patch was created successfully, but cleanup reported a warning:\n\n"+err.Error(), controller.window)
				return
			}
			dialog.ShowInformation("Patch created", "The VIPR patch was created successfully.", controller.window)
		})
	}(options)
}

func (controller *creatorController) createOptions() (patch.CreateOptions, error) {
	filePairs := controller.pairs.Pairs()
	if len(filePairs) == 0 {
		return patch.CreateOptions{}, fmt.Errorf("add at least one source/target file pair")
	}
	if controller.outputDirectory == "" {
		return patch.CreateOptions{}, fmt.Errorf("select an output folder")
	}
	name, err := normalizeOutputName(controller.outputName.Text)
	if err != nil {
		return patch.CreateOptions{}, err
	}
	level, err := strconv.Atoi(controller.levelSelect.Selected)
	if err != nil {
		return patch.CreateOptions{}, fmt.Errorf("invalid compression level")
	}
	workerBudget, err := selectedWorkerBudget(controller.workerSelect.Selected)
	if err != nil {
		return patch.CreateOptions{}, err
	}
	return patch.CreateOptions{
		Files:            filePairs,
		OutputPath:       filepath.Join(controller.outputDirectory, name),
		CompressionLevel: level,
		Comment:          controller.comment.Text,
		CreateReverse:    controller.reverse.Checked,
		WorkDirectory:    controller.workDirectory,
		WorkerBudget:     workerBudget,
	}, nil
}

func selectedWorkerBudget(selected string) (int, error) {
	if selected == automaticWorkerOption {
		return 0, nil
	}
	workerBudget, err := strconv.Atoi(selected)
	if err != nil || workerBudget < 1 || !workerbudget.IsValid(workerBudget) {
		return 0, fmt.Errorf("invalid worker target; select Auto or a value between 1 and %d", workerbudget.Maximum())
	}
	return workerBudget, nil
}

func (controller *creatorController) setControlsEnabled(enabled bool) {
	controller.pairs.SetEnabled(enabled)
	if enabled {
		controller.selectOutputDirectory.Enable()
		controller.outputName.Enable()
		controller.levelSelect.Enable()
		controller.workerSelect.Enable()
		controller.comment.Enable()
		controller.reverse.Enable()
		controller.selectWorkDirectory.Enable()
		controller.resetWorkDirectory.Enable()
		controller.createButton.Enable()
		return
	}
	controller.selectOutputDirectory.Disable()
	controller.outputName.Disable()
	controller.levelSelect.Disable()
	controller.workerSelect.Disable()
	controller.comment.Disable()
	controller.reverse.Disable()
	controller.selectWorkDirectory.Disable()
	controller.resetWorkDirectory.Disable()
	controller.createButton.Disable()
}
