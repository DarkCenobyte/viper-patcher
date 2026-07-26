package patch

import (
	"errors"
	"fmt"
	"os"
)

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
