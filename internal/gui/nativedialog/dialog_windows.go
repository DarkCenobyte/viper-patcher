//go:build windows

package nativedialog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func nativeAvailable() bool {
	_, err := exec.LookPath("powershell.exe")
	return err == nil
}

func nativeOpenFile(options FileOptions) (string, error) {
	filter := "All files (*.*)|*.*"
	if len(options.Extensions) > 0 {
		patterns := make([]string, 0, len(options.Extensions))
		for _, extension := range normalizeExtensions(options.Extensions) {
			patterns = append(patterns, "*"+extension)
		}
		filter = fmt.Sprintf("Supported files (%s)|%s|All files (*.*)|*.*", strings.Join(patterns, ", "), strings.Join(patterns, ";"))
	}
	return runPowerShellDialog(fileDialogScript, map[string]string{
		"VIPR_DIALOG_TITLE":   options.Title,
		"VIPR_DIALOG_INITIAL": initialDirectory(options.InitialPath),
		"VIPR_DIALOG_FILTER":  filter,
	})
}

func nativeOpenDirectory(options DirectoryOptions) (string, error) {
	return runPowerShellDialog(directoryDialogScript, map[string]string{
		"VIPR_DIALOG_TITLE":   options.Title,
		"VIPR_DIALOG_INITIAL": initialDirectory(options.InitialPath),
	})
}

func runPowerShellDialog(script string, environment map[string]string) (string, error) {
	path, err := exec.LookPath("powershell.exe")
	if err != nil {
		return "", ErrUnavailable
	}
	command := exec.Command(path, "-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Env = os.Environ()
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return "", ErrCanceled
		}
		return "", fmt.Errorf("open native Windows dialog: %w", err)
	}
	selected := strings.TrimSpace(output.String())
	if selected == "" {
		return "", ErrCanceled
	}
	return selected, nil
}

const fileDialogScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName PresentationFramework
$dialog = New-Object Microsoft.Win32.OpenFileDialog
$dialog.Title = $env:VIPR_DIALOG_TITLE
$dialog.Filter = $env:VIPR_DIALOG_FILTER
if ($env:VIPR_DIALOG_INITIAL) {
    $dialog.InitialDirectory = $env:VIPR_DIALOG_INITIAL
}
if ($dialog.ShowDialog() -eq $true) {
    [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
    [Console]::Write($dialog.FileName)
    exit 0
}
exit 1
`

const directoryDialogScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = $env:VIPR_DIALOG_TITLE
$dialog.ShowNewFolderButton = $true
if ($env:VIPR_DIALOG_INITIAL) {
    $dialog.SelectedPath = $env:VIPR_DIALOG_INITIAL
}
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
    [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
    [Console]::Write($dialog.SelectedPath)
    exit 0
}
exit 1
`
