//go:build !windows && !darwin

package nativedialog

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func nativeAvailable() bool {
	_, err := exec.LookPath("zenity")
	return err == nil
}

func nativeOpenFile(options FileOptions) (string, error) {
	arguments := []string{"--file-selection"}
	arguments = appendZenityCommon(arguments, options.Title, options.InitialPath)
	if len(options.Extensions) > 0 {
		patterns := make([]string, 0, len(options.Extensions))
		for _, extension := range normalizeExtensions(options.Extensions) {
			patterns = append(patterns, "*"+extension)
		}
		arguments = append(arguments, "--file-filter=Supported files | "+strings.Join(patterns, " "))
		arguments = append(arguments, "--file-filter=All files | *")
	}
	return runZenity(arguments)
}

func nativeOpenDirectory(options DirectoryOptions) (string, error) {
	arguments := []string{"--file-selection", "--directory"}
	arguments = appendZenityCommon(arguments, options.Title, options.InitialPath)
	return runZenity(arguments)
}

func appendZenityCommon(arguments []string, title, initialPath string) []string {
	if title != "" {
		arguments = append(arguments, "--title="+title)
	}
	if directory := initialDirectory(initialPath); directory != "" {
		arguments = append(arguments, "--filename="+directory+"/")
	}
	return arguments
}

func runZenity(arguments []string) (string, error) {
	path, err := exec.LookPath("zenity")
	if err != nil {
		return "", ErrUnavailable
	}
	command := exec.Command(path, arguments...)
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return "", ErrCanceled
		}
		return "", fmt.Errorf("open native Linux dialog: %w", err)
	}
	selected := strings.TrimSpace(output.String())
	if selected == "" {
		return "", ErrCanceled
	}
	return selected, nil
}
