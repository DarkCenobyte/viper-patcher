package patch

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func Apply(ctx context.Context, patchPath, root string, direction Direction, callback progress.Callback) error {
	return ApplyWithOptions(ctx, ApplyOptions{
		PatchPath: patchPath,
		Root:      root,
		Direction: direction,
	}, callback)
}

func Open(path string) (patchformat.Patch, error) {
	parsed, _, err := OpenWithDigest(path)
	return parsed, err
}

var defaultTransactionOperations = transactionOperations{
	createTemp: func(directory, pattern string) (*os.File, string, error) {
		file, err := os.CreateTemp(directory, pattern)
		if err != nil {
			return nil, "", err
		}
		return file, file.Name(), nil
	},
	rename: os.Rename,
	remove: os.Remove,
	verify: verifyFileExpectation,
}

func verifyFileExpectation(path string, expected fileExpectation) error {
	file, identity, err := openStableRegularFile(path)
	if err != nil {
		return fmt.Errorf("verify %q before replacement: %w", path, err)
	}
	verificationError := verifyOpenedFileExpectation(file, identity, path, expected)
	closeError := file.Close()
	return errors.Join(
		verificationError,
		wrapOperationError("close verified file", path, closeError),
	)
}
