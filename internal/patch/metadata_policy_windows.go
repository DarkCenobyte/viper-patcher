//go:build windows

package patch

import "io/fs"

func validateInstalledMetadata(mode fs.FileMode) error {
	if !mode.IsRegular() {
		return fs.ErrInvalid
	}
	return nil
}

func targetPermissions(fs.FileMode) fs.FileMode {
	// Unix permission bits have no portable meaning in the Windows patch model.
	return 0o666
}
