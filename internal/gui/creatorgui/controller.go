package creatorgui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/DarkCenobyte/viper-patcher/assets"
	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

type creatorController struct {
	window                fyne.Window
	pairs                 *filePairEditor
	levelSelect           *widget.Select
	comment               *widget.Entry
	reverse               *widget.Check
	outputDirectory       string
	outputDirectoryLabel  *widget.Label
	outputName            *widget.Entry
	selectOutputDirectory *widget.Button
	progressBar           *widget.ProgressBar
	status                *widget.Label
	createButton          *widget.Button
}

func newCreatorController(application fyne.App) *creatorController {
	controller := &creatorController{}
	controller.window = application.NewWindow("Viper Patcher - Creator")
	controller.window.SetIcon(assets.AppIcon)
	controller.window.Resize(fyne.NewSize(980, 760))

	controller.pairs = newFilePairEditor(controller.window)
	controller.levelSelect = widget.NewSelect(integerOptions(1, 22), nil)
	controller.levelSelect.SetSelected("3")
	controller.comment = widget.NewMultiLineEntry()
	controller.comment.SetText("Created with Viper-Patcher")
	controller.comment.SetMinRowsVisible(4)
	controller.reverse = widget.NewCheck("Generate reverse differentials", nil)
	controller.outputDirectoryLabel = widget.NewLabel("No output folder selected")
	controller.outputDirectoryLabel.Wrapping = fyne.TextWrapWord
	controller.outputName = widget.NewEntry()
	controller.outputName.SetText("update.vipr")
	controller.selectOutputDirectory = widget.NewButton("Choose output folder", controller.chooseOutputDirectory)
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
		"Each row is one permanent source-to-target association. Patch paths are derived from source files.",
		controller.pairs.Container(),
	)
	settingsCard := widget.NewCard(
		"Patch settings",
		"Configure compression, reverse support, and the embedded comment.",
		container.NewVBox(
			container.NewGridWithColumns(2,
				widget.NewLabel("Compression level"), controller.levelSelect,
				widget.NewLabel("Reverse support"), controller.reverse,
			),
			widget.NewLabel("Patch comment"),
			controller.comment,
		),
	)
	outputCard := widget.NewCard(
		"Output",
		"The core writes through a temporary file and safely replaces an existing regular patch.",
		container.NewVBox(
			container.NewBorder(nil, nil, controller.selectOutputDirectory, nil, controller.outputDirectoryLabel),
			container.NewGridWithColumns(2, widget.NewLabel("Filename"), controller.outputName),
		),
	)
	return container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("Create a differential patch", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewLabel("Add each source file together with the exact target file that replaces it."),
		),
		container.NewVBox(controller.progressBar, controller.status, controller.createButton),
		nil,
		nil,
		container.NewVScroll(container.NewVBox(pairCard, settingsCard, outputCard, layout.NewSpacer())),
	)
}

func (controller *creatorController) chooseOutputDirectory() {
	dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, controller.window)
			return
		}
		if uri == nil {
			return
		}
		controller.outputDirectory = uri.Path()
		controller.outputDirectoryLabel.SetText(controller.outputDirectory)
	}, controller.window).Show()
}

func (controller *creatorController) createPatch() {
	options, err := controller.createOptions()
	if err != nil {
		dialog.ShowError(err, controller.window)
		return
	}
	controller.setControlsEnabled(false)
	controller.progressBar.SetValue(0)
	controller.progressBar.Show()
	controller.status.SetText("Preparing immutable input snapshots...")

	go func(options patch.CreateOptions) {
		err := patch.Create(context.Background(), options, func(event progress.Event) {
			fyne.Do(func() {
				controller.status.SetText(creatorProgressText(event))
				controller.progressBar.SetValue(creatorOverallProgress(event, options.CreateReverse))
			})
		})
		fyne.Do(func() {
			controller.setControlsEnabled(true)
			if err != nil {
				controller.status.SetText("Patch creation failed.")
				dialog.ShowError(err, controller.window)
				return
			}
			controller.progressBar.SetValue(1)
			controller.status.SetText("Patch created successfully: " + options.OutputPath)
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
	name := strings.TrimSpace(controller.outputName.Text)
	if filepath.Ext(name) == "" {
		name += ".vipr"
	}
	if !strings.EqualFold(filepath.Ext(name), ".vipr") || filepath.Base(name) != name {
		return patch.CreateOptions{}, fmt.Errorf("output filename must be a simple .vipr filename")
	}
	level, err := strconv.Atoi(controller.levelSelect.Selected)
	if err != nil {
		return patch.CreateOptions{}, fmt.Errorf("invalid compression level")
	}
	return patch.CreateOptions{
		Files:            filePairs,
		OutputPath:       filepath.Join(controller.outputDirectory, name),
		CompressionLevel: level,
		Comment:          controller.comment.Text,
		CreateReverse:    controller.reverse.Checked,
	}, nil
}

func (controller *creatorController) setControlsEnabled(enabled bool) {
	controller.pairs.SetEnabled(enabled)
	if enabled {
		controller.selectOutputDirectory.Enable()
		controller.outputName.Enable()
		controller.levelSelect.Enable()
		controller.comment.Enable()
		controller.reverse.Enable()
		controller.createButton.Enable()
		return
	}
	controller.selectOutputDirectory.Disable()
	controller.outputName.Disable()
	controller.levelSelect.Disable()
	controller.comment.Disable()
	controller.reverse.Disable()
	controller.createButton.Disable()
}
