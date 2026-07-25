package appmode

import (
	"runtime"
	"testing"
)

func TestHeadlessRequested(t *testing.T) {
	for _, arguments := range [][]string{{"--headless"}, {"-headless"}, {"--output", "x", "--headless"}} {
		if !HeadlessRequested(arguments) {
			t.Fatalf("HeadlessRequested(%q) = false", arguments)
		}
	}
	if HeadlessRequested([]string{"--help"}) {
		t.Fatal("--help must not be treated as --headless")
	}
}

func TestCLIRequested(t *testing.T) {
	if CLIRequested(nil) {
		t.Fatal("nil arguments must select automatic GUI mode")
	}
	if !CLIRequested([]string{"--help"}) {
		t.Fatal("arguments must select CLI mode")
	}
}

func TestGUIAvailable(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		t.Setenv("DISPLAY", "")
		t.Setenv("WAYLAND_DISPLAY", "")
		if GUIAvailable() {
			t.Fatal("GUIAvailable returned true without a display")
		}
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		if !GUIAvailable() {
			t.Fatal("GUIAvailable returned false with Wayland")
		}
	case "windows", "darwin":
		if !GUIAvailable() {
			t.Fatal("desktop platform should be considered GUI-capable")
		}
	}
}
