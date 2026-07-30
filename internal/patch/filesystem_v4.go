package patch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// openStableRegular is used for creator inputs and patch files outside an
// installation root. Installed files use installationRoot and os.Root.
func openStableRegular(path string) (*os.File, fs.FileInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 0 {
		return nil, nil, fmt.Errorf("%q is not a stable regular file", path)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !os.SameFile(info, opened) {
		file.Close()
		return nil, nil, fmt.Errorf("%q changed while opening", path)
	}
	return file, opened, nil
}

func stableUnchanged(file *os.File, path string, identity fs.FileInfo) error {
	current, err := file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, current) || !os.SameFile(identity, pathInfo) || current.Size() != identity.Size() || !current.ModTime().Equal(identity.ModTime()) {
		return fmt.Errorf("%q changed during operation", path)
	}
	return nil
}

func wrapOperationError(operation, path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %q: %w", operation, path, err)
}

func stableRootUnchanged(root *installationRoot, file *os.File, name string, identity fs.FileInfo) error {
	if root == nil || root.root == nil || file == nil || identity == nil {
		return fmt.Errorf("installation file identity is unavailable")
	}
	if err := rejectSymlinkComponents(root.root, name); err != nil {
		return fmt.Errorf("verify %q before replacement: %w", name, err)
	}
	current, err := file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := root.root.Lstat(name)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, current) || !os.SameFile(identity, pathInfo) || current.Size() != identity.Size() || !current.ModTime().Equal(identity.ModTime()) {
		return fmt.Errorf("%q changed during operation", name)
	}
	return nil
}

type preparedFile struct {
	path, temp, backup string
	identity           fs.FileInfo
}

func reserveRootPath(root *installationRoot, directory, prefix string) (string, error) {
	file, name, err := createRootTemp(root.root, directory, prefix)
	if err != nil {
		return "", err
	}
	closeErr := file.Close()
	removeErr := root.root.Remove(name)
	if closeErr != nil || removeErr != nil {
		return "", errors.Join(closeErr, removeErr)
	}
	return name, nil
}

func commitPrepared(root *installationRoot, files []preparedFile, durability DurabilityMode) (resultErr error) {
	committed := 0
	allCommitted := false
	defer func() {
		if resultErr == nil || allCommitted {
			return
		}
		var rollbackErrors []error
		for i := committed - 1; i >= 0; i-- {
			if err := root.root.Remove(files[i].path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove replacement %q: %w", files[i].path, err))
				continue
			}
			if err := root.root.Rename(files[i].backup, files[i].path); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore %q: %w", files[i].path, err))
			}
		}
		for i := committed; i < len(files); i++ {
			if err := root.root.Remove(files[i].temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove abandoned output %q: %w", files[i].temp, err))
			}
		}
		if rollbackErr := errors.Join(rollbackErrors...); rollbackErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback after failed V4 commit: %w", rollbackErr))
		}
	}()

	for i := range files {
		item := &files[i]
		current, _, currentName, err := root.openStableRegularFile(filepath.ToSlash(item.path))
		if err != nil {
			return fmt.Errorf("reopen %q before replacement: %w", item.path, err)
		}
		if currentName != item.path {
			_ = current.Close()
			return fmt.Errorf("reopened path changed from %q to %q", item.path, currentName)
		}
		verifyErr := stableRootUnchanged(root, current, item.path, item.identity)
		closeErr := current.Close()
		if err := errors.Join(
			wrapOperationError("verify installed source before replacement", item.path, verifyErr),
			wrapOperationError("close installed source before replacement", item.path, closeErr),
		); err != nil {
			return err
		}

		backup, err := reserveRootPath(root, filepath.Dir(item.path), ".viper-v4-backup-")
		if err != nil {
			return fmt.Errorf("reserve backup for %q: %w", item.path, err)
		}
		item.backup = backup
		if err := root.root.Rename(item.path, item.backup); err != nil {
			return fmt.Errorf("backup %q: %w", item.path, err)
		}
		if err := root.root.Rename(item.temp, item.path); err != nil {
			replaceErr := fmt.Errorf("replace %q: %w", item.path, err)
			if restoreErr := root.root.Rename(item.backup, item.path); restoreErr != nil {
				return errors.Join(
					replaceErr,
					fmt.Errorf("restore %q from retained backup %q: %w", item.path, item.backup, restoreErr),
				)
			}
			return replaceErr
		}
		committed++
		if durability == DurabilityDurable {
			if err := syncDirectoryV4(root, filepath.Dir(item.path)); err != nil {
				return err
			}
		}
	}
	allCommitted = true

	var cleanup []error
	for i := range files {
		err := root.root.Remove(files[i].backup)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanup = append(cleanup, fmt.Errorf("remove backup %q: %w", files[i].backup, err))
		}
	}
	if durability == DurabilityDurable {
		seen := make(map[string]struct{}, len(files))
		for i := range files {
			directory := filepath.Clean(filepath.Dir(files[i].path))
			if _, exists := seen[directory]; exists {
				continue
			}
			seen[directory] = struct{}{}
			if err := syncDirectoryV4(root, directory); err != nil {
				cleanup = append(cleanup, err)
			}
		}
	}
	return committedWarning("patch application", errors.Join(cleanup...))
}
