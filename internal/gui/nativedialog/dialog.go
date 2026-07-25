// Package nativedialog provides native file and directory selection with a Fyne fallback.
package nativedialog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
)

// ErrUnavailable indicates that no native dialog provider is available.
var ErrUnavailable = errors.New("native file dialog is unavailable")

// ErrCanceled indicates that the user canceled the dialog.
var ErrCanceled = errors.New("native file dialog was canceled")

// FileOptions configures a file selection dialog.
type FileOptions struct {
	Title       string
	InitialPath string
	Extensions  []string
}

// DirectoryOptions configures a directory selection dialog.
type DirectoryOptions struct {
	Title       string
	InitialPath string
}

// OpenFile opens a native file dialog when available and falls back to Fyne otherwise.
func OpenFile(parent fyne.Window, options FileOptions, callback func(string, error)) {
	if !nativeAvailable() {
		openFyneFile(parent, options, callback)
		return
	}
	go func() {
		path, err := nativeOpenFile(options)
		fyne.Do(func() {
			handleNativeResult(err, func() {
				openFyneFile(parent, options, callback)
			}, func() {
				callback(path, nil)
			}, callback)
		})
	}()
}

// OpenDirectory opens a native directory dialog when available and falls back to Fyne otherwise.
func OpenDirectory(parent fyne.Window, options DirectoryOptions, callback func(string, error)) {
	if !nativeAvailable() {
		openFyneDirectory(parent, options, callback)
		return
	}
	go func() {
		path, err := nativeOpenDirectory(options)
		fyne.Do(func() {
			handleNativeResult(err, func() {
				openFyneDirectory(parent, options, callback)
			}, func() {
				callback(path, nil)
			}, callback)
		})
	}()
}

func handleNativeResult(err error, fallback, success func(), callback func(string, error)) {
	switch {
	case err == nil:
		success()
	case errors.Is(err, ErrCanceled):
		callback("", nil)
	case errors.Is(err, ErrUnavailable):
		fallback()
	default:
		fallback()
	}
}

func openFyneFile(parent fyne.Window, options FileOptions, callback func(string, error)) {
	picker := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			callback("", err)
			return
		}
		if reader == nil {
			callback("", nil)
			return
		}
		path := reader.URI().Path()
		if err := reader.Close(); err != nil {
			callback("", fmt.Errorf("close selected file: %w", err))
			return
		}
		callback(path, nil)
	}, parent)
	if len(options.Extensions) > 0 {
		picker.SetFilter(storage.NewExtensionFileFilter(normalizeExtensions(options.Extensions)))
	}
	configureFyneDialog(picker, options.Title, options.InitialPath)
	picker.Show()
}

func openFyneDirectory(parent fyne.Window, options DirectoryOptions, callback func(string, error)) {
	picker := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			callback("", err)
			return
		}
		if uri == nil {
			callback("", nil)
			return
		}
		callback(uri.Path(), nil)
	}, parent)
	configureFyneDialog(picker, options.Title, options.InitialPath)
	picker.Show()
}

func configureFyneDialog(picker *dialog.FileDialog, title, initialPath string) {
	if title != "" {
		picker.SetTitleText(title)
	}
	directory := initialDirectory(initialPath)
	if directory == "" {
		return
	}
	location, err := storage.ListerForURI(storage.NewFileURI(directory))
	if err == nil {
		picker.SetLocation(location)
	}
}

func normalizeExtensions(extensions []string) []string {
	normalized := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		if extension == "" {
			continue
		}
		if extension[0] != '.' {
			extension = "." + extension
		}
		normalized = append(normalized, extension)
	}
	return normalized
}

func initialDirectory(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	if info, err := os.Stat(cleaned); err == nil {
		if info.IsDir() {
			return cleaned
		}
		return filepath.Dir(cleaned)
	}
	if filepath.Ext(cleaned) != "" {
		return filepath.Dir(cleaned)
	}
	return cleaned
}
