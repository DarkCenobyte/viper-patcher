package creatorgui

import (
	"fmt"
	"path/filepath"
	"strings"
)

func normalizeOutputName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", fmt.Errorf("output filename must not be empty")
	}
	if filepath.Ext(name) == "" {
		name += ".vipr"
	}
	if !strings.EqualFold(filepath.Ext(name), ".vipr") || filepath.Base(name) != name || strings.EqualFold(name, ".vipr") {
		return "", fmt.Errorf("output filename must be a simple .vipr filename")
	}
	return name, nil
}
