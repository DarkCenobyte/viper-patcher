//go:build ignore

package patch

import (
	"errors"
	"fmt"
	"os"
)

func syncRootDirectory(root *os.Root, path string) error {
	if root == nil {
		return fmt.Errorf("directory root is unavailable")
	}
	if path == "" {
		path = "."
	}
	directory, err := root.Open(path)
	if err != nil {
		return fmt.Errorf("open directory %q for synchronization: %w", path, err)
	}
	syncError := directory.Sync()
	closeError := directory.Close()
	return errors.Join(
		wrapOperationError("sync directory", path, syncError),
		wrapOperationError("close synchronized directory", path, closeError),
	)
}
