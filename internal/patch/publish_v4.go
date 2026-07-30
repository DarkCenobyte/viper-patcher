package patch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// publishCreatedPatch replaces destination with tempName without using a
// predictable user-visible backup name. A failed restoration deliberately
// leaves the transaction directory intact and reports its exact location.
func publishCreatedPatch(tempName, destination string) (committed bool, resultErr error) {
	directory := filepath.Dir(destination)
	transactionDirectory, err := os.MkdirTemp(directory, ".viper-v4-publish-*")
	if err != nil {
		return false, fmt.Errorf("create patch publication transaction: %w", err)
	}
	backup := filepath.Join(transactionDirectory, "previous-output")
	hadOriginal := false

	if _, err := os.Lstat(destination); err == nil {
		if err := os.Rename(destination, backup); err != nil {
			cleanupErr := os.RemoveAll(transactionDirectory)
			return false, errors.Join(
				fmt.Errorf("backup existing patch %q: %w", destination, err),
				wrapOperationError("remove publication transaction", transactionDirectory, cleanupErr),
			)
		}
		hadOriginal = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		cleanupErr := os.RemoveAll(transactionDirectory)
		return false, errors.Join(
			fmt.Errorf("inspect existing patch %q: %w", destination, err),
			wrapOperationError("remove publication transaction", transactionDirectory, cleanupErr),
		)
	}

	if err := os.Rename(tempName, destination); err != nil {
		replaceErr := fmt.Errorf("publish patch %q: %w", destination, err)
		if hadOriginal {
			if restoreErr := os.Rename(backup, destination); restoreErr != nil {
				return false, errors.Join(
					replaceErr,
					fmt.Errorf("restore previous patch %q from %q: %w", destination, backup, restoreErr),
				)
			}
		}
		cleanupErr := os.RemoveAll(transactionDirectory)
		return false, errors.Join(
			replaceErr,
			wrapOperationError("remove publication transaction", transactionDirectory, cleanupErr),
		)
	}

	committed = true
	installSyncErr := syncCreatedPatchDirectory(destination)
	cleanupErr := os.RemoveAll(transactionDirectory)
	var cleanupSyncErr error
	if cleanupErr == nil {
		cleanupSyncErr = syncCreatedPatchDirectory(destination)
	}
	return true, committedWarning(
		"patch creation",
		installSyncErr,
		wrapOperationError("remove publication transaction", transactionDirectory, cleanupErr),
		cleanupSyncErr,
	)
}
