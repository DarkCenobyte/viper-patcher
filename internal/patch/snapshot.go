package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type fileExpectation struct {
	Identity os.FileInfo
	Hash     string
	Size     uint64
}

type fileSnapshot struct {
	SnapshotPath     string
	SnapshotIdentity os.FileInfo
	Hash             string
	Size             uint64
	Mode             uint32
	Identity         os.FileInfo
}

type stableFileSource struct {
	display string
	open    func() (*os.File, os.FileInfo, error)
	lstat   func() (os.FileInfo, error)
}

var errSnapshotContentMismatch = errors.New("snapshot content does not match expectation")

type snapshotContentExpectation struct {
	hash    string
	size    uint64
	hasSize bool
}

func snapshotRegularFile(sourcePath, destinationPath string) (fileSnapshot, error) {
	source := stableFileSource{
		display: sourcePath,
		open: func() (*os.File, os.FileInfo, error) {
			return openStableRegularFile(sourcePath)
		},
		lstat: func() (os.FileInfo, error) {
			return os.Lstat(sourcePath)
		},
	}
	return snapshotStableFile(source, destinationPath, nil)
}

func snapshotInstallationFile(root *installationRoot, patchPath, destinationPath string, expected snapshotContentExpectation) (fileSnapshot, string, error) {
	var localized string
	source := stableFileSource{
		display: patchPath,
		open: func() (*os.File, os.FileInfo, error) {
			file, info, name, err := root.openStableRegularFile(patchPath)
			if err == nil {
				localized = name
			}
			return file, info, err
		},
		lstat: func() (os.FileInfo, error) {
			if localized == "" {
				return nil, fmt.Errorf("localized path is unavailable")
			}
			return root.root.Lstat(localized)
		},
	}
	snapshot, err := snapshotStableFile(source, destinationPath, &expected)
	return snapshot, localized, err
}

func snapshotStableFile(source stableFileSource, destinationPath string, expected *snapshotContentExpectation) (snapshot fileSnapshot, resultError error) {
	file, identity, err := source.open()
	if err != nil {
		return fileSnapshot{}, err
	}
	sourceClosed := false
	defer func() {
		if !sourceClosed {
			if closeError := file.Close(); closeError != nil {
				resultError = errors.Join(resultError, fmt.Errorf("close snapshot source %q: %w", source.display, closeError))
			}
		}
	}()

	output, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("create snapshot for %q: %w", source.display, err)
	}
	outputClosed := false
	committed := false
	defer func() {
		if !outputClosed {
			if closeError := output.Close(); closeError != nil {
				resultError = errors.Join(resultError, fmt.Errorf("close incomplete snapshot for %q: %w", source.display, closeError))
			}
		}
		if !committed {
			if removeError := os.Remove(destinationPath); removeError != nil && !os.IsNotExist(removeError) {
				resultError = errors.Join(resultError, fmt.Errorf("remove incomplete snapshot for %q: %w", source.display, removeError))
			}
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(output, hash), file)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("snapshot %q: %w", source.display, err)
	}
	if written < 0 || identity.Size() < 0 || uint64(written) != uint64(identity.Size()) {
		return fileSnapshot{}, fmt.Errorf("file %q changed size while it was being snapshotted", source.display)
	}
	snapshotHash := hex.EncodeToString(hash.Sum(nil))
	if expected != nil {
		if snapshotHash != expected.hash || (expected.hasSize && uint64(written) != expected.size) {
			return fileSnapshot{}, fmt.Errorf("%w: file %q", errSnapshotContentMismatch, source.display)
		}
		if err := verifySnapshotSourceMetadata(file, source, identity, uint64(written)); err != nil {
			return fileSnapshot{}, err
		}
	} else if err := verifySnapshotSource(file, source, identity, snapshotHash, uint64(written)); err != nil {
		return fileSnapshot{}, err
	}
	if err := output.Sync(); err != nil {
		return fileSnapshot{}, fmt.Errorf("sync snapshot for %q: %w", source.display, err)
	}
	if err := output.Close(); err != nil {
		return fileSnapshot{}, fmt.Errorf("close snapshot for %q: %w", source.display, err)
	}
	outputClosed = true
	snapshotIdentity, err := os.Stat(destinationPath)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("inspect snapshot for %q: %w", source.display, err)
	}
	if !snapshotIdentity.Mode().IsRegular() || snapshotIdentity.Size() < 0 || uint64(snapshotIdentity.Size()) != uint64(written) {
		return fileSnapshot{}, fmt.Errorf("snapshot for %q is not a stable regular file", source.display)
	}
	if err := file.Close(); err != nil {
		return fileSnapshot{}, fmt.Errorf("close snapshot source %q: %w", source.display, err)
	}
	sourceClosed = true
	committed = true
	return fileSnapshot{
		SnapshotPath:     destinationPath,
		SnapshotIdentity: snapshotIdentity,
		Hash:             snapshotHash,
		Size:             uint64(written),
		Mode:             uint32(identity.Mode().Perm()),
		Identity:         identity,
	}, nil
}

func copyPatchSnapshot(sourcePath, workDirectory, expectedHash string) (fileSnapshot, error) {
	destination := filepath.Join(workDirectory, "patch.vipr")
	var expected *snapshotContentExpectation
	if expectedHash != "" {
		expected = &snapshotContentExpectation{hash: expectedHash}
	}
	source := stableFileSource{
		display: sourcePath,
		open: func() (*os.File, os.FileInfo, error) {
			return openStableRegularFile(sourcePath)
		},
		lstat: func() (os.FileInfo, error) {
			return os.Lstat(sourcePath)
		},
	}
	snapshot, err := snapshotStableFile(source, destination, expected)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("snapshot patch file: %w", err)
	}
	return snapshot, nil
}

func openStableRegularFile(path string) (*os.File, os.FileInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %q: %w", path, err)
	}
	linkInfo, err := os.Lstat(absolute)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect %q: %w", path, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%q is a symbolic link", path)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, nil, fmt.Errorf("open %q: %w", path, err)
	}
	closeWithError := func(operationError error) error {
		closeError := file.Close()
		if closeError == nil {
			return operationError
		}
		return errors.Join(operationError, fmt.Errorf("close %q: %w", path, closeError))
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, closeWithError(fmt.Errorf("inspect opened file %q: %w", path, err))
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return nil, nil, closeWithError(fmt.Errorf("%q is not a regular file", path))
	}
	if !os.SameFile(linkInfo, info) {
		return nil, nil, closeWithError(fmt.Errorf("%q changed while it was being opened", path))
	}
	return file, info, nil
}

func verifyRootFileExpectation(root *os.Root, path string, expected fileExpectation) error {
	if err := rejectSymlinkComponents(root, path); err != nil {
		return fmt.Errorf("verify %q before replacement: %w", path, err)
	}
	linkInfo, err := root.Lstat(path)
	if err != nil {
		return fmt.Errorf("verify %q before replacement: %w", path, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("verify %q before replacement: symbolic link", path)
	}
	file, err := root.Open(path)
	if err != nil {
		return fmt.Errorf("verify %q before replacement: %w", path, err)
	}
	identity, statError := file.Stat()
	var verificationError error
	switch {
	case statError != nil:
		verificationError = fmt.Errorf("verify %q before replacement: %w", path, statError)
	case !os.SameFile(linkInfo, identity):
		verificationError = fmt.Errorf("%q was replaced after validation", path)
	default:
		verificationError = verifyOpenedFileExpectation(file, identity, path, expected)
	}
	closeError := file.Close()
	return errors.Join(
		verificationError,
		wrapOperationError("close verified file", path, closeError),
	)
}

func verifyOpenedFileExpectation(file *os.File, identity os.FileInfo, displayPath string, expected fileExpectation) error {
	if !os.SameFile(identity, expected.Identity) {
		return fmt.Errorf("%q was replaced after validation", displayPath)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("verify content of %q: %w", displayPath, err)
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if size < 0 || uint64(size) != expected.Size || actualHash != expected.Hash {
		return fmt.Errorf("%q changed after validation", displayPath)
	}
	return nil
}

func verifySnapshotSource(file *os.File, source stableFileSource, identity os.FileInfo, expectedHash string, expectedSize uint64) error {
	if err := verifySnapshotSourceMetadata(file, source, identity, expectedSize); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind %q after snapshot: %w", source.display, err)
	}
	hash := sha256.New()
	verifiedSize, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("verify %q after snapshot: %w", source.display, err)
	}
	if verifiedSize < 0 || uint64(verifiedSize) != expectedSize || hex.EncodeToString(hash.Sum(nil)) != expectedHash {
		return fmt.Errorf("file %q changed while it was being snapshotted", source.display)
	}
	return nil
}

func verifySnapshotSourceMetadata(file *os.File, source stableFileSource, identity os.FileInfo, expectedSize uint64) error {
	currentInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect %q after snapshot: %w", source.display, err)
	}
	if !os.SameFile(identity, currentInfo) || currentInfo.Size() < 0 || uint64(currentInfo.Size()) != expectedSize ||
		!currentInfo.ModTime().Equal(identity.ModTime()) {
		return fmt.Errorf("file %q changed while it was being snapshotted", source.display)
	}
	pathInfo, err := source.lstat()
	if err != nil {
		return fmt.Errorf("inspect %q after snapshot: %w", source.display, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, pathInfo) {
		return fmt.Errorf("file %q was replaced while it was being snapshotted", source.display)
	}
	return nil
}
