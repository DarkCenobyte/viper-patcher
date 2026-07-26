package creatorgui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/DarkCenobyte/viper-patcher/assets"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/branding"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/nativedialog"
	"github.com/DarkCenobyte/viper-patcher/internal/gui/windowsizing"
	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

const (
	creatorWindowWidth          = 1040
	creatorWindowFallbackHeight = 940
	creatorLogoWidth            = 360
	creatorLogoHeight           = 166
)

type creatorController struct {
	window                fyne.Window
	header                fyne.CanvasObject
	body                  fyne.CanvasObject
	footer                fyne.CanvasObject
	pairs                 *filePairEditor
	levelSelect           *widget.Select
	compressionWarning    *widget.Label
	parallelSelect        *widget.Select
	comment               *widget.Entry
	reverse               *widget.Check
	outputDirectory       string
	outputDirectoryLabel  *widget.Label
	outputName            *widget.Entry
	selectOutputDirectory *widget.Button
	workDirectory         string
	workDirectoryLabel    *widget.Label
	selectWorkDirectory   *widget.Button
	resetWorkDirectory    *widget.Button
	progressBar           *widget.ProgressBar
	lastProgress          float64
	status                *widget.Label
	createButton          *widget.Button
}

func newCreatorController(application fyne.App) *creatorController {
	controller := &creatorController{}
	controller.window = application.NewWindow("Viper Patcher - Creator")
	controller.window.SetIcon(assets.AppIcon)
	controller.pairs = newFilePairEditor(controller.window)
	controller.compressionWarning = widget.NewLabel("Warning: ultra compression levels 20-22 can severely impact performance, with much longer patch creation times and high CPU usage.")
	controller.compressionWarning.Wrapping = fyne.TextWrapWord
	controller.compressionWarning.TextStyle = fyne.TextStyle{Bold: true}
	controller.compressionWarning.Importance = widget.WarningImportance
	controller.compressionWarning.Hide()
	controller.levelSelect = widget.NewSelect(integerOptions(1, 22), controller.updateCompressionWarning)
	controller.levelSelect.SetSelected("3")
	controller.parallelSelect = widget.NewSelect(integerOptions(1, runtime.NumCPU()), nil)
	controller.parallelSelect.SetSelected("1")
	controller.comment = widget.NewMultiLineEntry()
	controller.comment.SetText("Created with Viper-Patcher")
	controller.comment.SetMinRowsVisible(3)
	controller.reverse = widget.NewCheck("Generate reverse differentials", nil)
	controller.outputDirectoryLabel = widget.NewLabel("No output folder selected")
	controller.outputDirectoryLabel.Wrapping = fyne.TextWrapWord
	controller.outputName = widget.NewEntry()
	controller.outputName.SetText("update.vipr")
	controller.selectOutputDirectory = widget.NewButton("Choose output folder", controller.chooseOutputDirectory)
	controller.workDirectoryLabel = widget.NewLabel("System temporary directory")
	controller.workDirectoryLabel.Wrapping = fyne.TextWrapWord
	controller.selectWorkDirectory = widget.NewButton("Choose work folder", controller.chooseWorkDirectory)
	controller.resetWorkDirectory = widget.NewButton("Use system default", controller.clearWorkDirectory)
	controller.progressBar = widget.NewProgressBar()
	controller.progressBar.Hide()
	controller.status = widget.NewLabel("Ready. Add at least one source/target file pair.")
	controller.status.Wrapping = fyne.TextWrapWord
	controller.createButton = widget.NewButton("Create patch", controller.createPatch)
	controller.createButton.Importance = widget.HighImportance
	controller.window.SetContent(controller.buildContent())
	return controller
}

func (controller *creatorController) buildContent() fyne.CanvasObject {
	pairCard := widget.NewCard(
		"Source and target file pairs",
		"Each row is one permanent source-to-target association. Paths are displayed relative to the common folder of each column.",
		controller.pairs.Container(),
	)
	commentCard := widget.NewCard(
		"Comment",
		"This text is embedded in the patch and displayed by Viper-Patcher before application.",
		controller.comment,
	)
	settingsContent := container.NewVBox(
		container.NewGridWithColumns(2, widget.NewLabel("Compression level"), controller.levelSelect),
		controller.compressionWarning,
		container.NewGridWithColumns(2, widget.NewLabel("Parallel files"), controller.parallelSelect),
		container.NewGridWithColumns(2, widget.NewLabel("Reverse support"), controller.reverse),
		container.NewBorder(nil, nil, container.NewHBox(controller.selectWorkDirectory, controller.resetWorkDirectory), nil, controller.workDirectoryLabel),
	)
	settings := newCollapsibleSection("Settings", settingsContent, func(open bool) {
		if open {
			controller.growWindowToFitContent()
		}
	})
	outputCard := widget.NewCard(
		"Output",
		"The core writes through a temporary file and safely replaces an existing regular patch.",
		container.NewVBox(
			container.NewBorder(nil, nil, controller.selectOutputDirectory, nil, controller.outputDirectoryLabel),
			container.NewGridWithColumns(2, widget.NewLabel("Filename"), controller.outputName),
		),
	)
	controller.header = container.NewVBox(
		branding.NewLogo(assets.AppLogo, fyne.NewSize(creatorLogoWidth, creatorLogoHeight)),
		widget.NewLabelWithStyle("Create a differential patch", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Add each source file together with the exact target file that replaces it.", fyne.TextAlignCenter, fyne.TextStyle{}),
	)
	controller.body = container.NewVBox(pairCard, commentCard, settings.Container(), outputCard, layout.NewSpacer())
	controller.footer = container.NewVBox(controller.progressBar, controller.status, controller.createButton)
	return container.NewBorder(
		controller.header,
		controller.footer,
		nil,
		nil,
		container.NewVScroll(controller.body),
	)
}

func (controller *creatorController) updateCompressionWarning(selected string) {
	if !isUltraCompressionLevel(selected) {
		controller.compressionWarning.Hide()
		return
	}

	controller.compressionWarning.Show()
	controller.growWindowToFitContent()
}

func isUltraCompressionLevel(selected string) bool {
	level, err := strconv.Atoi(selected)
	return err == nil && level >= 20 && level <= 22
}

func (controller *creatorController) fitInitialWindow() {
	maximumHeight := windowsizing.MaximumContentHeight(controller.window, creatorWindowFallbackHeight)
	controller.pairs.SetTableHeight(filePairTablePreferredHeight)

	desiredHeight := windowsizing.PreferredBorderContentHeight(controller.header, controller.body, controller.footer)
	tableHeight := fittedFilePairTableHeight(
		filePairTablePreferredHeight,
		filePairTableMinimumHeight,
		desiredHeight,
		maximumHeight,
	)
	controller.pairs.SetTableHeight(tableHeight)
	desiredHeight = windowsizing.PreferredBorderContentHeight(controller.header, controller.body, controller.footer)
	if desiredHeight > maximumHeight {
		desiredHeight = maximumHeight
	}

	controller.window.Resize(fyne.NewSize(creatorWindowWidth, desiredHeight))
	controller.window.CenterOnScreen()
}

func (controller *creatorController) growWindowToFitContent() {
	desiredHeight := windowsizing.PreferredBorderContentHeight(controller.header, controller.body, controller.footer)
	maximumHeight := windowsizing.MaximumContentHeight(controller.window, creatorWindowFallbackHeight)
	if desiredHeight > maximumHeight {
		desiredHeight = maximumHeight
	}

	currentSize := controller.window.Canvas().Size()
	if desiredHeight <= currentSize.Height {
		return
	}
	width := currentSize.Width
	if width < creatorWindowWidth {
		width = creatorWindowWidth
	}
	controller.window.Resize(fyne.NewSize(width, desiredHeight))
}

func (controller *creatorController) chooseOutputDirectory() {
	nativedialog.OpenDirectory(controller.window, nativedialog.DirectoryOptions{
		Title:       "Choose output folder",
		InitialPath: controller.outputDirectory,
	}, func(path string, err error) {
		if err != nil {
			dialog.ShowError(err, controller.window)
			return
		}
		if path == "" {
			return
		}
		controller.outputDirectory = path
		controller.outputDirectoryLabel.SetText(path)
	})
}

func (controller *creatorController) chooseWorkDirectory() {
	nativedialog.OpenDirectory(controller.window, nativedialog.DirectoryOptions{
		Title:       "Choose creator work folder",
		InitialPath: controller.workDirectory,
	}, func(path string, err error) {
		if err != nil {
			dialog.ShowError(err, controller.window)
			return
		}
		if path == "" {
			return
		}
		controller.workDirectory = path
		controller.workDirectoryLabel.SetText(path)
	})
}

func (controller *creatorController) clearWorkDirectory() {
	controller.workDirectory = ""
	controller.workDirectoryLabel.SetText("System temporary directory")
}

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
	controller.lastProgress = 0
	controller.progressBar.SetValue(0)
	controller.progressBar.Show()
	controller.status.SetText("Preparing immutable input snapshots...")

	go func(options patch.CreateOptions) {
		err := patch.Create(context.Background(), options, func(event progress.Event) {
			fyne.Do(func() {
				controller.status.SetText(creatorProgressText(event))
				value := creatorOverallProgress(event, options.CreateReverse)
				if value > controller.lastProgress {
					controller.lastProgress = value
					controller.progressBar.SetValue(value)
				}
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
	parallelism, err := strconv.Atoi(controller.parallelSelect.Selected)
	if err != nil {
		return patch.CreateOptions{}, fmt.Errorf("invalid parallel file count")
	}
	return patch.CreateOptions{
		Files:            filePairs,
		OutputPath:       filepath.Join(controller.outputDirectory, name),
		CompressionLevel: level,
		Comment:          controller.comment.Text,
		CreateReverse:    controller.reverse.Checked,
		WorkDirectory:    controller.workDirectory,
		Parallelism:      parallelism,
	}, nil
}

func (controller *creatorController) setControlsEnabled(enabled bool) {
	controller.pairs.SetEnabled(enabled)
	if enabled {
		controller.selectOutputDirectory.Enable()
		controller.outputName.Enable()
		controller.levelSelect.Enable()
		controller.parallelSelect.Enable()
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
	controller.parallelSelect.Disable()
	controller.comment.Disable()
	controller.reverse.Disable()
	controller.selectWorkDirectory.Disable()
	controller.resetWorkDirectory.Disable()
	controller.createButton.Disable()
}
