package appmode

import (
	"os"
	"runtime"
	"strings"
)

// GUIAvailable performs a conservative runtime check without initializing a GUI toolkit.
func GUIAvailable() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	case "linux", "freebsd", "openbsd", "netbsd":
		return strings.TrimSpace(os.Getenv("DISPLAY")) != "" || strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""
	default:
		return false
	}
}

// HeadlessRequested reports whether --headless is present.
func HeadlessRequested(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--headless" || argument == "-headless" {
			return true
		}
	}
	return false
}

// CLIRequested reports whether command-line arguments request non-GUI behavior.
func CLIRequested(arguments []string) bool {
	return len(arguments) > 0
}
