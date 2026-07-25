//go:build darwin

package nativedialog

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func nativeAvailable() bool {
	_, err := exec.LookPath("osascript")
	return err == nil
}

func nativeOpenFile(options FileOptions) (string, error) {
	command := "choose file"
	if options.Title != "" {
		command += " with prompt " + appleScriptString(options.Title)
	}
	if directory := initialDirectory(options.InitialPath); directory != "" {
		command += " default location POSIX file " + appleScriptString(directory)
	}
	return runAppleScript("POSIX path of (" + command + ")")
}

func nativeOpenDirectory(options DirectoryOptions) (string, error) {
	command := "choose folder"
	if options.Title != "" {
		command += " with prompt " + appleScriptString(options.Title)
	}
	if directory := initialDirectory(options.InitialPath); directory != "" {
		command += " default location POSIX file " + appleScriptString(directory)
	}
	return runAppleScript("POSIX path of (" + command + ")")
}

func runAppleScript(script string) (string, error) {
	path, err := exec.LookPath("osascript")
	if err != nil {
		return "", ErrUnavailable
	}
	command := exec.Command(path, "-e", script)
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && bytes.Contains(exitError.Stderr, []byte("User canceled")) {
			return "", ErrCanceled
		}
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return "", ErrCanceled
		}
		return "", fmt.Errorf("open native macOS dialog: %w", err)
	}
	selected := strings.TrimSpace(output.String())
	if selected == "" {
		return "", ErrCanceled
	}
	return selected, nil
}

func appleScriptString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}
