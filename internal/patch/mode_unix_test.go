//go:build ignore

package patch

import (
	"os"
	"testing"
)

func TestApplyOutputModePreservesInstalledMode(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "mode-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var actual os.FileMode
	err = applyOutputMode(file, 0o4640, func(_ *os.File, mode os.FileMode) error {
		actual = mode
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if actual != 0o640 {
		t.Fatalf("mode = %#o, want 0640", actual)
	}
	if !outputModeMatches(0o640, 0o4640) || outputModeMatches(0o600, 0o640) {
		t.Fatal("Unix output mode matching must compare masked local permission bits")
	}
}
