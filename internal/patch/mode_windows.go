//go:build windows

package patch

import "os"

func applyOutputMode(*os.File, uint32, func(*os.File, os.FileMode) error) error {
	// VIPR patches are cross-platform. Windows file modes are intentionally
	// ignored, because os.Chmod cannot represent Unix permission bits reliably.
	return nil
}

func outputModeMatches(uint32, uint32) bool {
	return true
}
