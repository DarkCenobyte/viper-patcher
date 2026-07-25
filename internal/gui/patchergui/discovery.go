package patchergui

import (
	"os"
	"path/filepath"
	"strings"
)

func adjacentPatchPath(executablePath string) (string, bool, error) {
	if executablePath == "" {
		return "", false, nil
	}
	directory := filepath.Dir(executablePath)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", false, err
	}
	var match string
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".vipr") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", false, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if match != "" {
			return "", false, nil
		}
		match = filepath.Join(directory, entry.Name())
	}
	return match, match != "", nil
}
