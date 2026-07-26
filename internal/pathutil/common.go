package pathutil

import (
	"fmt"
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

func isWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
