package patch

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type installationRoot struct {
	path string
	root *os.Root
}

func openInstallationRoot(path string) (*installationRoot, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve target root: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect target root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("target root %q is a symbolic link", path)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("target root %q is not a directory", path)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open target root: %w", err)
	}
	openedInfo, err := root.Stat(".")
	if err != nil {
		closeError := root.Close()
		return nil, errors.Join(
			fmt.Errorf("inspect opened target root: %w", err),
			wrapOperationError("close", absolute, closeError),
		)
	}
	if !os.SameFile(info, openedInfo) {
		closeError := root.Close()
		return nil, errors.Join(
			fmt.Errorf("target root %q changed while it was being opened", path),
			wrapOperationError("close", absolute, closeError),
		)
	}
	return &installationRoot{path: absolute, root: root}, nil
}

func (root *installationRoot) Close() error {
	if root == nil || root.root == nil {
		return nil
	}
	return root.root.Close()
}

func localPatchPath(patchPath string) (string, error) {
	localized, err := filepath.Localize(patchPath)
	if err != nil {
		return "", fmt.Errorf("localize patch path %q: %w", patchPath, err)
	}
	if localized == "." || !filepath.IsLocal(localized) {
		return "", fmt.Errorf("unsafe patch path %q", patchPath)
	}
	return localized, nil
}

func (root *installationRoot) openStableRegularFile(patchPath string) (*os.File, os.FileInfo, string, error) {
	name, err := localPatchPath(patchPath)
	if err != nil {
		return nil, nil, "", err
	}
	if err := rejectSymlinkComponents(root.root, name); err != nil {
		return nil, nil, "", fmt.Errorf("inspect %q: %w", patchPath, err)
	}
	linkInfo, err := root.root.Lstat(name)
	if err != nil {
		return nil, nil, "", err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, "", fmt.Errorf("%q is a symbolic link", patchPath)
	}
	file, err := root.root.Open(name)
	if err != nil {
		return nil, nil, "", err
	}
	closeWithError := func(operationError error) error {
		return errors.Join(operationError, wrapOperationError("close", patchPath, file.Close()))
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, "", closeWithError(err)
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return nil, nil, "", closeWithError(fmt.Errorf("%q is not a regular file", patchPath))
	}
	if !os.SameFile(linkInfo, info) {
		return nil, nil, "", closeWithError(fmt.Errorf("%q changed while it was being opened", patchPath))
	}
	return file, info, name, nil
}

func rejectSymlinkComponents(root *os.Root, name string) error {
	current := ""
	for _, component := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		if current == "" {
			current = component
		} else {
			current = filepath.Join(current, component)
		}
		info, err := root.Lstat(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path traverses symbolic link %q", current)
		}
	}
	return nil
}

func createRootTemp(root *os.Root, directory, prefix string) (*os.File, string, error) {
	if directory == "" {
		directory = "."
	}
	for range 100 {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", fmt.Errorf("generate temporary filename: %w", err)
		}
		name := filepath.Join(directory, prefix+hex.EncodeToString(random[:]))
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			return file, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not allocate a unique temporary filename")
}
