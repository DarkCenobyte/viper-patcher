package creatorgui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

// Run starts the creator graphical interface.
func Run() {
	application := app.NewWithID("io.github.viperpatcher.creator")
	window := application.NewWindow("Viper Patcher - Creator")
	window.Resize(fyne.NewSize(920, 700))

	sources := newFileSelector("Source files", window)
	targets := newFileSelector("Target files", window)
	levelSelect := widget.NewSelect(integerOptions(1, 22), nil)
	levelSelect.SetSelected("3")
	comment := widget.NewMultiLineEntry()
	comment.SetText("REPLACEME")
	comment.SetMinRowsVisible(5)
	reverse := widget.NewCheck("Generate a reverse patch", nil)
	progressBar := widget.NewProgressBar()
	progressBar.Hide()
	status := widget.NewLabel("Ready.")
	status.Wrapping = fyne.TextWrapWord

	createButton := widget.NewButton("Create patch", nil)
	createButton.Importance = widget.HighImportance
	createButton.OnTapped = func() {
		if len(sources.Files()) == 0 || len(targets.Files()) == 0 {
			dialog.ShowError(fmt.Errorf("select at least one source file and one target file"), window)
			return
		}
		if len(sources.Files()) != len(targets.Files()) {
			dialog.ShowError(fmt.Errorf("source files and target files must have exactly the same count"), window)
			return
		}
		level, err := strconv.Atoi(levelSelect.Selected)
		if err != nil {
			dialog.ShowError(fmt.Errorf("invalid compression level"), window)
			return
		}

		saveDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, saveErr error) {
			if saveErr != nil {
				dialog.ShowError(saveErr, window)
				return
			}
			if writer == nil {
				return
			}
			outputPath := writer.URI().Path()
			_ = writer.Close()
			_ = os.Remove(outputPath)
			if filepath.Ext(outputPath) == "" {
				outputPath += ".vipr"
			}
			if !equalExtension(outputPath, ".vipr") {
				dialog.ShowError(fmt.Errorf("patch output must use the .vipr extension"), window)
				return
			}

			createButton.Disable()
			progressBar.SetValue(0)
			progressBar.Show()
			status.SetText("Preparing patch...")
			sourceFiles := sources.Files()
			targetFiles := targets.Files()
			patchComment := comment.Text
			createReverse := reverse.Checked
			go func() {
				err := patch.Create(context.Background(), patch.CreateOptions{
					SourceFiles:      sourceFiles,
					TargetFiles:      targetFiles,
					OutputPath:       outputPath,
					CompressionLevel: level,
					Comment:          patchComment,
					CreateReverse:    createReverse,
				}, func(event progress.Event) {
					fyne.Do(func() {
						status.SetText(progressText(event))
						progressBar.SetValue(overallProgress(event, createReverse))
					})
				})
				fyne.Do(func() {
					createButton.Enable()
					if err != nil {
						status.SetText("Patch creation failed.")
						dialog.ShowError(err, window)
						return
					}
					progressBar.SetValue(1)
					status.SetText("Patch created successfully: " + outputPath)
					dialog.ShowInformation("Patch created", "The VIPR patch was created successfully.", window)
				})
			}()
		}, window)
		saveDialog.SetFileName("update.vipr")
		saveDialog.SetFilter(storage.NewExtensionFileFilter([]string{".vipr"}))
		saveDialog.Show()
	}

	settings := container.NewGridWithColumns(2,
		widget.NewLabel("Compression level"), levelSelect,
		widget.NewLabel("Reverse support"), reverse,
	)
	content := container.NewBorder(
		container.NewVBox(widget.NewLabelWithStyle("Create a differential patch", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})),
		container.NewVBox(progressBar, status, createButton),
		nil,
		nil,
		container.NewVScroll(container.NewVBox(
			container.NewGridWithColumns(2, sources.Container(), targets.Container()),
			widget.NewSeparator(),
			settings,
			widget.NewLabel("Patch comment"),
			comment,
			layout.NewSpacer(),
		)),
	)
	window.SetContent(content)
	window.ShowAndRun()
}

type fileSelector struct {
	files    []string
	selected int
	list     *widget.List
	box      *fyne.Container
	window   fyne.Window
}

func newFileSelector(title string, window fyne.Window) *fileSelector {
	selector := &fileSelector{selected: -1, window: window}
	selector.list = widget.NewList(
		func() int { return len(selector.files) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.ListItemID, object fyne.CanvasObject) {
			object.(*widget.Label).SetText(selector.files[id])
		},
	)
	selector.list.OnSelected = func(id widget.ListItemID) { selector.selected = id }
	selector.list.OnUnselected = func(widget.ListItemID) { selector.selected = -1 }
	add := widget.NewButton("Add file", func() {
		dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			if reader == nil {
				return
			}
			path := reader.URI().Path()
			_ = reader.Close()
			selector.files = append(selector.files, path)
			selector.list.Refresh()
		}, window)
	})
	remove := widget.NewButton("Remove selected", func() {
		if selector.selected < 0 || selector.selected >= len(selector.files) {
			return
		}
		selector.files = append(selector.files[:selector.selected], selector.files[selector.selected+1:]...)
		selector.selected = -1
		selector.list.UnselectAll()
		selector.list.Refresh()
	})
	clear := widget.NewButton("Clear", func() {
		selector.files = nil
		selector.selected = -1
		selector.list.UnselectAll()
		selector.list.Refresh()
	})
	selector.box = container.NewBorder(
		widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(add, remove, clear),
		nil,
		nil,
		selector.list,
	)
	selector.box.Resize(fyne.NewSize(430, 300))
	return selector
}

func (selector *fileSelector) Files() []string {
	return append([]string(nil), selector.files...)
}

func (selector *fileSelector) Container() fyne.CanvasObject { return selector.box }

func integerOptions(minimum, maximum int) []string {
	values := make([]string, 0, maximum-minimum+1)
	for value := minimum; value <= maximum; value++ {
		values = append(values, strconv.Itoa(value))
	}
	return values
}

func progressText(event progress.Event) string {
	if event.Stage == "completed" {
		return "Finalizing patch..."
	}
	if event.Path == "" {
		return event.Stage
	}
	return fmt.Sprintf("[%d/%d] %s: %s", event.FileIndex, event.FileCount, event.Stage, event.Path)
}

func overallProgress(event progress.Event, includeReverse bool) float64 {
	if event.Stage == "completed" {
		return 1
	}
	if event.FileCount <= 0 || event.FileIndex <= 0 {
		return 0
	}
	unitsPerFile := 1
	if includeReverse {
		unitsPerFile = 2
	}
	completedUnits := (event.FileIndex - 1) * unitsPerFile
	if includeReverse && event.Stage == "compressing-reverse" {
		completedUnits++
	}
	if event.Stage == "file-completed" {
		completedUnits += unitsPerFile
	}
	fraction := 0.0
	if event.Stage != "file-completed" && event.TotalBytes > 0 {
		fraction = float64(event.ProcessedBytes) / float64(event.TotalBytes)
	}
	value := (float64(completedUnits) + fraction) / float64(event.FileCount*unitsPerFile)
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func equalExtension(path, extension string) bool {
	return len(filepath.Ext(path)) == len(extension) && stringsEqualFold(filepath.Ext(path), extension)
}

func stringsEqualFold(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
