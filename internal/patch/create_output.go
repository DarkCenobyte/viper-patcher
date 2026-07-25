package patch

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func assemblePatch(outputPath string, header patchformat.Header, blobs []differentialBlobs) (resultError error) {
	outputDirectory := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporaryOutput, err := os.CreateTemp(outputDirectory, ".viper-patcher-*.vipr.tmp")
	if err != nil {
		return fmt.Errorf("create temporary patch: %w", err)
	}
	temporaryPath := temporaryOutput.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			if closeError := temporaryOutput.Close(); closeError != nil {
				resultError = errors.Join(resultError, fmt.Errorf("close incomplete patch output: %w", closeError))
			}
		}
		if !committed {
			if removeError := os.Remove(temporaryPath); removeError != nil && !os.IsNotExist(removeError) {
				resultError = errors.Join(resultError, fmt.Errorf("remove incomplete patch output %q: %w", temporaryPath, removeError))
			}
		}
	}()

	if _, err := patchformat.EncodePrefix(temporaryOutput, header); err != nil {
		return err
	}
	for _, blob := range blobs {
		if err := appendFile(temporaryOutput, blob.forward); err != nil {
			return err
		}
		if blob.reverse != "" {
			if err := appendFile(temporaryOutput, blob.reverse); err != nil {
				return err
			}
		}
	}
	if err := temporaryOutput.Sync(); err != nil {
		return fmt.Errorf("sync patch file: %w", err)
	}
	if err := temporaryOutput.Close(); err != nil {
		return fmt.Errorf("close patch file: %w", err)
	}
	closed = true
	if err := replaceOutput(temporaryPath, outputPath); err != nil {
		return err
	}
	committed = true
	return nil
}

func appendFile(output io.Writer, path string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyError := io.Copy(output, input)
	closeError := input.Close()
	return errors.Join(
		wrapOperationError("append", path, copyError),
		wrapOperationError("close", path, closeError),
	)
}

func wrapOperationError(operation, path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %q: %w", operation, path, err)
}

func replaceOutput(source, destination string) error {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("commit patch output: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing output: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("existing output %q is not a regular file", destination)
	}

	digest, size, err := hashutil.File(destination)
	if err != nil {
		return fmt.Errorf("hash existing output: %w", err)
	}
	transaction := NewTransaction()
	if err := transaction.Add(destination, source, fileExpectation{
		Identity: info,
		Hash:     digest,
		Size:     size,
		Mode:     uint32(info.Mode().Perm()),
	}); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		cleanupError := transaction.Cleanup()
		return errors.Join(fmt.Errorf("commit patch output: %w", err), cleanupError)
	}
	return nil
}
