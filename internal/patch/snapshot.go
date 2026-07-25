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
	Mode     uint32
}

type fileSnapshot struct {
	OriginalPath string
	SnapshotPath string
	Hash         string
	Size         uint64
	Mode         uint32
	Identity     os.FileInfo
}

func snapshotRegularFile(sourcePath, destinationPath string) (snapshot fileSnapshot, resultError error) {
	file, identity, err := openStableRegularFile(sourcePath)
	if err != nil {
		return fileSnapshot{}, err
	}
	sourceClosed := false
	defer func() {
		if !sourceClosed {
			if closeError := file.Close(); closeError != nil {
				resultError = errors.Join(resultError, fmt.Errorf("close snapshot source %q: %w", sourcePath, closeError))
			}
		}
	}()

	output, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("create snapshot for %q: %w", sourcePath, err)
	}
	outputClosed := false
	committed := false
	defer func() {
		if !outputClosed {
			if closeError := output.Close(); closeError != nil {
				resultError = errors.Join(resultError, fmt.Errorf("close incomplete snapshot for %q: %w", sourcePath, closeError))
			}
		}
		if !committed {
			if removeError := os.Remove(destinationPath); removeError != nil && !os.IsNotExist(removeError) {
				resultError = errors.Join(resultError, fmt.Errorf("remove incomplete snapshot for %q: %w", sourcePath, removeError))
			}
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(output, hash), file)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("snapshot %q: %w", sourcePath, err)
	}
	if written < 0 || identity.Size() < 0 || uint64(written) != uint64(identity.Size()) {
		return fileSnapshot{}, fmt.Errorf("file %q changed size while it was being snapshotted", sourcePath)
	}
	snapshotHash := hex.EncodeToString(hash.Sum(nil))
	if err := verifySnapshotSource(file, sourcePath, identity, snapshotHash, uint64(written)); err != nil {
		return fileSnapshot{}, err
	}
	if err := output.Sync(); err != nil {
		return fileSnapshot{}, fmt.Errorf("sync snapshot for %q: %w", sourcePath, err)
	}
	if err := output.Close(); err != nil {
		return fileSnapshot{}, fmt.Errorf("close snapshot for %q: %w", sourcePath, err)
	}
	outputClosed = true
	if err := file.Close(); err != nil {
		return fileSnapshot{}, fmt.Errorf("close snapshot source %q: %w", sourcePath, err)
	}
	sourceClosed = true
	committed = true
	return fileSnapshot{
		OriginalPath: sourcePath,
		SnapshotPath: destinationPath,
		Hash:         snapshotHash,
		Size:         uint64(written),
		Mode:         uint32(identity.Mode().Perm()),
		Identity:     identity,
	}, nil
}

func copyPatchSnapshot(sourcePath, workDirectory string) (fileSnapshot, error) {
	destination := filepath.Join(workDirectory, "patch.vipr")
	snapshot, err := snapshotRegularFile(sourcePath, destination)
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

func verifyFileExpectation(path string, expected fileExpectation) error {
	file, identity, err := openStableRegularFile(path)
	if err != nil {
		return fmt.Errorf("verify %q before replacement: %w", path, err)
	}
	defer file.Close()
	if !os.SameFile(identity, expected.Identity) {
		return fmt.Errorf("%q was replaced after validation", path)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("verify content of %q: %w", path, err)
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if uint64(size) != expected.Size || actualHash != expected.Hash || uint32(identity.Mode().Perm()) != expected.Mode {
		return fmt.Errorf("%q changed after validation", path)
	}
	return nil
}

func verifySnapshotSource(file *os.File, sourcePath string, identity os.FileInfo, expectedHash string, expectedSize uint64) error {
	currentInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect %q after snapshot: %w", sourcePath, err)
	}
	if !os.SameFile(identity, currentInfo) || currentInfo.Size() < 0 || uint64(currentInfo.Size()) != expectedSize ||
		currentInfo.Mode().Perm() != identity.Mode().Perm() || !currentInfo.ModTime().Equal(identity.ModTime()) {
		return fmt.Errorf("file %q changed while it was being snapshotted", sourcePath)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind %q after snapshot: %w", sourcePath, err)
	}
	hash := sha256.New()
	verifiedSize, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("verify %q after snapshot: %w", sourcePath, err)
	}
	if verifiedSize < 0 || uint64(verifiedSize) != expectedSize || hex.EncodeToString(hash.Sum(nil)) != expectedHash {
		return fmt.Errorf("file %q changed while it was being snapshotted", sourcePath)
	}
	pathInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return fmt.Errorf("inspect %q after snapshot: %w", sourcePath, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, pathInfo) {
		return fmt.Errorf("file %q was replaced while it was being snapshotted", sourcePath)
	}
	return nil
}
