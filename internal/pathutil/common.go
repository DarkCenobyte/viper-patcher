package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CommonBase returns the deepest common directory containing all files.
func CommonBase(files []string) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("at least one file is required")
	}

	absolute := make([]string, len(files))
	for index, path := range files {
		resolved, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", path, err)
		}
		absolute[index] = filepath.Clean(resolved)
	}

	base := filepath.Dir(absolute[0])
	for _, path := range absolute[1:] {
		candidate := filepath.Dir(path)
		for !isWithin(base, candidate) {
			parent := filepath.Dir(base)
			if parent == base {
				return "", fmt.Errorf("source files do not share a common filesystem root")
			}
			base = parent
		}
	}
	return base, nil
}

// RelativePatchPath returns a slash-separated safe path relative to base.
func RelativePatchPath(base, file string) (string, error) {
	relative, err := filepath.Rel(base, file)
	if err != nil {
		return "", fmt.Errorf("make %q relative to %q: %w", file, base, err)
	}
	relative = filepath.Clean(relative)
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("unsafe relative path %q", relative)
	}
	return filepath.ToSlash(relative), nil
}

// SecureJoin joins a canonical slash-separated patch path to root and rejects traversal.
func SecureJoin(root, patchPath string) (string, error) {
	if patchPath == "" || strings.ContainsRune(patchPath, '\x00') || strings.Contains(patchPath, "\\") {
		return "", fmt.Errorf("invalid patch path %q", patchPath)
	}
	converted := filepath.FromSlash(patchPath)
	cleaned := filepath.Clean(converted)
	if filepath.ToSlash(cleaned) != patchPath || cleaned == "." || cleaned == ".." || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("unsafe or non-canonical patch path %q", patchPath)
	}
	joined := filepath.Join(root, cleaned)
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absoluteJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if !isWithin(absoluteRoot, absoluteJoined) {
		return "", fmt.Errorf("patch path escapes target directory: %q", patchPath)
	}
	return absoluteJoined, nil
}

// SecureJoinExisting rejects symbolic links in every existing path component.
// Missing trailing components are allowed so callers can report a normal missing-file state.
func SecureJoinExisting(root, patchPath string) (string, error) {
	joined, err := SecureJoin(root, patchPath)
	if err != nil {
		return "", err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve target root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", fmt.Errorf("resolve target root links: %w", err)
	}
	rootInfo, err := os.Stat(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("inspect target root: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("target root %q is not a directory", root)
	}
	relative, err := filepath.Rel(absoluteRoot, joined)
	if err != nil {
		return "", fmt.Errorf("resolve patch path relative to root: %w", err)
	}

	current := resolvedRoot
	components := strings.Split(relative, string(filepath.Separator))
	for index, component := range components {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				for _, remaining := range components[index+1:] {
					current = filepath.Join(current, remaining)
				}
				return current, nil
			}
			return "", fmt.Errorf("inspect path component %q: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("patch path %q traverses symbolic link %q", patchPath, current)
		}
	}
	return current, nil
}

func isWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
