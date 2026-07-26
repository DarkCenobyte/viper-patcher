//go:build !windows

package patch

import "os"

func applyOutputMode(file *os.File, preservedMode uint32, chmod func(*os.File, os.FileMode) error) error {
	if chmod == nil {
		return os.ErrInvalid
	}
	return chmod(file, os.FileMode(preservedMode&portablePermissionMask))
}

func outputModeMatches(actual, preserved uint32) bool {
	return actual&portablePermissionMask == preserved&portablePermissionMask
}
