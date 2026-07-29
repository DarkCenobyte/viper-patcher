//go:build ignore

package patch

import (
	"os"
	"testing"
)

func TestWindowsOutputModesAreIgnored(t *testing.T) {
	called := false
	if err := applyOutputMode(nil, 0o755, func(*os.File, os.FileMode) error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("Windows must not attempt to reproduce Unix permission bits")
	}
	if !outputModeMatches(0o200, 0o755) {
		t.Fatal("permission bits must not block cross-platform patches on Windows")
	}
}
