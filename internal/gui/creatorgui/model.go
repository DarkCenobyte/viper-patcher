package creatorgui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
)

type filePairDisplay struct {
	Source string
	Target string
}

type filePairModel struct {
	mutex sync.RWMutex
	pairs []patch.FilePair
}

func (model *filePairModel) Add(sourcePath, targetPath string) error {
	if sourcePath == "" || targetPath == "" {
		return fmt.Errorf("both source and target files are required")
	}
	model.mutex.Lock()
	defer model.mutex.Unlock()
	model.pairs = append(model.pairs, patch.FilePair{SourcePath: sourcePath, TargetPath: targetPath})
	return nil
}

func (model *filePairModel) Remove(index int) bool {
	model.mutex.Lock()
	defer model.mutex.Unlock()
	if index < 0 || index >= len(model.pairs) {
		return false
	}
	model.pairs = append(model.pairs[:index], model.pairs[index+1:]...)
	return true
}

func (model *filePairModel) Clear() {
	model.mutex.Lock()
	defer model.mutex.Unlock()
	model.pairs = nil
}

func (model *filePairModel) Pairs() []patch.FilePair {
	model.mutex.RLock()
	defer model.mutex.RUnlock()
	return append([]patch.FilePair(nil), model.pairs...)
}

func (model *filePairModel) DisplayPairs() []filePairDisplay {
	return buildFilePairDisplay(model.Pairs())
}

func buildFilePairDisplay(pairs []patch.FilePair) []filePairDisplay {
	sourcePaths := make([]string, len(pairs))
	targetPaths := make([]string, len(pairs))
	for index, pair := range pairs {
		sourcePaths[index] = pair.SourcePath
		targetPaths[index] = pair.TargetPath
	}
	sourceRoot := commonFileDirectory(sourcePaths)
	targetRoot := commonFileDirectory(targetPaths)
	display := make([]filePairDisplay, len(pairs))
	for index, pair := range pairs {
		display[index] = filePairDisplay{
			Source: displayPath(sourceRoot, pair.SourcePath),
			Target: displayPath(targetRoot, pair.TargetPath),
		}
	}
	return display
}

func commonFileDirectory(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	common := filepath.Dir(filepath.Clean(paths[0]))
	for _, path := range paths[1:] {
		directory := filepath.Dir(filepath.Clean(path))
		for !sameOrDescendant(common, directory) {
			parent := filepath.Dir(common)
			if parent == common {
				return ""
			}
			common = parent
		}
	}
	return common
}

func sameOrDescendant(base, candidate string) bool {
	relative, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func displayPath(base, path string) string {
	cleaned := filepath.Clean(path)
	if base == "" {
		return filepath.ToSlash(cleaned)
	}
	relative, err := filepath.Rel(base, cleaned)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(cleaned)
	}
	return filepath.ToSlash(relative)
}
