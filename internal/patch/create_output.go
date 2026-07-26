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

func assemblePatch(outputPath string, header patchformat.Header, blobs []differentialBlobs) error {
	outputDirectory := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	outputRoot, err := os.OpenRoot(outputDirectory)
	if err != nil {
		return fmt.Errorf("open output directory: %w", err)
	}

	temporaryOutput, temporaryName, operationError := createRootTemp(outputRoot, ".", ".viper-patcher-patch-")
	if operationError == nil {
		operationError = writePatchOutput(temporaryOutput, header, blobs)
	}
	if operationError == nil {
		operationError = replaceRootOutput(outputRoot, temporaryName, filepath.Base(outputPath))
	}

	committed := operationError == nil || IsCommittedWarning(operationError)
	var temporaryCleanupError error
	if !committed {
		temporaryCleanupError = wrapRootRemoveError(outputRoot, temporaryName)
	}
	rootCloseError := outputRoot.Close()
	if !committed {
		return errors.Join(
			operationError,
			wrapJoinedError("remove incomplete patch output", temporaryCleanupError),
			wrapJoinedError("close output directory", rootCloseError),
		)
	}
	return committedWarning(
		"patch output",
		operationError,
		wrapJoinedError("close output directory", rootCloseError),
	)
}

func writePatchOutput(output *os.File, header patchformat.Header, blobs []differentialBlobs) error {
	if output == nil {
		return fmt.Errorf("patch output file is unavailable")
	}
	if _, err := patchformat.EncodePrefix(output, header); err != nil {
		return errors.Join(err, wrapOperationError("close incomplete patch output", output.Name(), output.Close()))
	}
	for _, blob := range blobs {
		if err := appendFile(output, blob.forward); err != nil {
			return errors.Join(err, wrapOperationError("close incomplete patch output", output.Name(), output.Close()))
		}
		if blob.reverse != "" {
			if err := appendFile(output, blob.reverse); err != nil {
				return errors.Join(err, wrapOperationError("close incomplete patch output", output.Name(), output.Close()))
			}
		}
	}
	if err := output.Sync(); err != nil {
		return errors.Join(
			fmt.Errorf("sync patch file: %w", err),
			wrapOperationError("close incomplete patch output", output.Name(), output.Close()),
		)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close patch file: %w", err)
	}
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

func replaceRootOutput(root *os.Root, source, destination string) error {
	info, err := root.Lstat(destination)
	if os.IsNotExist(err) {
		if err := root.Rename(source, destination); err != nil {
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

	expectation, err := rootFileExpectation(root, destination, info)
	if err != nil {
		return fmt.Errorf("inspect existing output: %w", err)
	}
	transaction := newRootTransaction(root)
	if err := transaction.Add(destination, source, expectation); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		cleanupError := transaction.Cleanup()
		return errors.Join(fmt.Errorf("commit patch output: %w", err), cleanupError)
	}
	return nil
}

func rootFileExpectation(root *os.Root, path string, linkInfo os.FileInfo) (fileExpectation, error) {
	file, err := root.Open(path)
	if err != nil {
		return fileExpectation{}, err
	}
	identity, statError := file.Stat()
	if statError != nil {
		return fileExpectation{}, errors.Join(statError, wrapOperationError("close", path, file.Close()))
	}
	if !identity.Mode().IsRegular() || identity.Size() < 0 || !os.SameFile(linkInfo, identity) {
		return fileExpectation{}, errors.Join(
			fmt.Errorf("existing output changed while it was being opened"),
			wrapOperationError("close", path, file.Close()),
		)
	}
	digest, size, hashError := hashutil.Reader(file)
	currentInfo, currentError := file.Stat()
	pathInfo, pathError := root.Lstat(path)
	closeError := file.Close()
	if hashError != nil || currentError != nil || pathError != nil || closeError != nil {
		return fileExpectation{}, errors.Join(
			wrapJoinedError("hash existing output", hashError),
			wrapJoinedError("inspect existing output after hashing", currentError),
			wrapJoinedError("inspect existing output path after hashing", pathError),
			wrapOperationError("close", path, closeError),
		)
	}
	if !os.SameFile(identity, currentInfo) || !os.SameFile(identity, pathInfo) || currentInfo.Size() < 0 || uint64(currentInfo.Size()) != size ||
		!currentInfo.ModTime().Equal(identity.ModTime()) {
		return fileExpectation{}, fmt.Errorf("existing output changed while it was being hashed")
	}
	return fileExpectation{Identity: identity, Hash: digest, Size: size}, nil
}
