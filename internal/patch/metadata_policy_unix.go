//go:build !windows

package patch

import (
	"fmt"
	"io/fs"
)

const unsupportedInstalledMode = fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

func validateInstalledMetadata(mode fs.FileMode) error {
	if !mode.IsRegular() {
		return fmt.Errorf("installed input is not a regular file")
	}
	if unsupported := mode & unsupportedInstalledMode; unsupported != 0 {
		return fmt.Errorf("installed file uses unsupported privilege mode bits %s", unsupported)
	}
	return nil
}

func targetPermissions(mode fs.FileMode) fs.FileMode {
	return mode.Perm()
}
