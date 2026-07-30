//go:build !windows

package patch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func syncCreatedPatchDirectory(path string) error {
	directoryPath := filepath.Dir(path)
	directory, err := os.Open(directoryPath)
	if err != nil {
		return fmt.Errorf("open patch output directory for synchronization: %w", err)
	}
	return errors.Join(
		wrapOperationError("sync patch output directory", directoryPath, directory.Sync()),
		wrapOperationError("close patch output directory", directoryPath, directory.Close()),
	)
}
